package com.turbobutton.app;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.job.JobInfo;
import android.app.job.JobParameters;
import android.app.job.JobScheduler;
import android.app.job.JobService;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.content.pm.Signature;
import android.net.Uri;
import android.os.Build;
import android.widget.Toast;

import androidx.core.content.FileProvider;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Finds new builds on the GitHub release "latest", tells the technician, and downloads and
 * installs them. A background job checks about twice a day and posts a notification.
 */
public final class Updater {
    static final String REPO = "flesheatingbug/Turbo-Button";
    static final String TAG = "latest";
    static final String ASSET = "TurboButton.apk";
    static final String ACTION_UPDATE = "com.turbobutton.app.UPDATE";
    private static final int JOB_ID = 4711;

    private Updater() { }

    static final class Info {
        int build; String notes = "", url = "", error;
        Info(String error) { this.error = error; }
        Info(int build, String notes, String url) { this.build = build; this.notes = notes; this.url = url; }
    }

    static SharedPreferences prefs(Context c) { return c.getSharedPreferences("updates", Context.MODE_PRIVATE); }
    static boolean auto(Context c) { return prefs(c).getBoolean("auto", true); }

    static void setAuto(Context c, boolean on) {
        prefs(c).edit().putBoolean("auto", on).apply();
        schedule(c);
    }

    @SuppressWarnings("deprecation")
    static int current(Context c) {
        try {
            PackageInfo pi = c.getPackageManager().getPackageInfo(c.getPackageName(), 0);
            return Build.VERSION.SDK_INT >= 28 ? (int) pi.getLongVersionCode() : pi.versionCode;
        } catch (Exception e) { return 0; }
    }

    private static HttpURLConnection open(String url, String accept) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setConnectTimeout(10000);
        c.setReadTimeout(30000);
        c.setRequestProperty("Accept", accept);
        c.setRequestProperty("User-Agent", "turbobutton-android");
        return c;
    }

    /** Blocking: run it off the main thread. */
    static Info check() {
        try {
            HttpURLConnection c = open("https://api.github.com/repos/" + REPO + "/releases/tags/" + TAG, "application/vnd.github+json");
            int code = c.getResponseCode();
            if (code == 404) return new Info("No published build yet.");
            if (code == 403 || code == 429) return new Info("GitHub is limiting requests right now. Try again in a while.");
            if (code < 200 || code > 299) return new Info("GitHub answered HTTP " + code);
            InputStream in = c.getInputStream();
            ByteArrayOutputStream bo = new ByteArrayOutputStream();
            byte[] buf = new byte[16384]; int n;
            while ((n = in.read(buf)) > 0) bo.write(buf, 0, n);
            in.close();
            JSONObject j = new JSONObject(new String(bo.toByteArray(), StandardCharsets.UTF_8));
            String notes = j.optString("body");
            Matcher m = Pattern.compile("[Bb]uild (\\d+)").matcher(notes);
            int build = m.find() ? Integer.parseInt(m.group(1)) : 0;
            JSONArray assets = j.optJSONArray("assets");
            String url = "";
            for (int i = 0; assets != null && i < assets.length(); i++) {
                JSONObject a = assets.getJSONObject(i);
                if (ASSET.equals(a.optString("name"))) url = a.optString("browser_download_url");
            }
            if (build == 0 || url.isEmpty()) return new Info("No downloadable app build found.");
            return new Info(build, notes, url);
        } catch (Exception e) {
            return new Info("Can't reach GitHub: " + e.getMessage());
        }
    }

    private static void toast(Activity a, String m) { a.runOnUiThread(() -> Toast.makeText(a, m, Toast.LENGTH_LONG).show()); }

    /** Checks and, if there's a newer build, asks. [manual] also reports "up to date" and errors. */
    static void checkAndPrompt(Activity act, boolean manual) {
        new Thread(() -> {
            Info r = check();
            act.runOnUiThread(() -> {
                if (act.isFinishing()) return;
                if (r.error != null) { if (manual) toast(act, r.error); return; }
                SharedPreferences p = prefs(act);
                p.edit().putLong("last", System.currentTimeMillis()).apply();
                int have = current(act);
                if (r.build <= have) { if (manual) toast(act, "You have the latest version (build " + have + ")."); return; }
                if (!manual && p.getInt("skipped", 0) == r.build) return;
                new AlertDialog.Builder(act)
                        .setIcon(R.mipmap.ic_launcher)
                        .setTitle("Update available")
                        .setMessage("Build " + r.build + " is ready. You have build " + have + ".")
                        .setPositiveButton("Download and install", (d, w) -> download(act, r))
                        .setNegativeButton("Later", (d, w) -> p.edit().putInt("skipped", r.build).apply())
                        .show();
            });
        }).start();
    }

    /** Called when the app opens: at most every 6 hours. */
    static void checkOnLaunch(Activity act) {
        if (!auto(act)) return;
        if (System.currentTimeMillis() - prefs(act).getLong("last", 0) < 6 * 3600_000L) return;
        checkAndPrompt(act, false);
    }

    private static void download(Activity act, Info r) {
        AlertDialog dlg = new AlertDialog.Builder(act).setIcon(R.mipmap.ic_launcher).setTitle("Downloading update").setMessage("Starting…").setCancelable(false).create();
        dlg.show();
        new Thread(() -> {
            try {
                File dir = new File(act.getCacheDir(), "updates");
                //noinspection ResultOfMethodCallIgnored
                dir.mkdirs();
                File[] old = dir.listFiles();
                if (old != null) for (File f : old) //noinspection ResultOfMethodCallIgnored
                    f.delete();
                File out = new File(dir, "TurboButton.apk");
                HttpURLConnection c = open(r.url, "application/octet-stream");
                c.setInstanceFollowRedirects(true);
                if (c.getResponseCode() < 200 || c.getResponseCode() > 299) throw new RuntimeException("HTTP " + c.getResponseCode());
                long total = c.getContentLengthLong(), done = 0;
                int shown = -1;
                try (InputStream in = c.getInputStream(); OutputStream o = new FileOutputStream(out)) {
                    byte[] buf = new byte[64 * 1024]; int n;
                    while ((n = in.read(buf)) > 0) {
                        o.write(buf, 0, n); done += n;
                        int pct = total > 0 ? (int) (done * 100 / total) : -1;
                        if (pct != shown) {
                            shown = pct;
                            final String msg = pct >= 0 ? pct + "%" : (done / 1024) + " KB";
                            act.runOnUiThread(() -> dlg.setMessage(msg));
                        }
                    }
                }
                final boolean sameKey = sameSigner(act, out);
                act.runOnUiThread(() -> {
                    dlg.dismiss();
                    if (!sameKey) {
                        new AlertDialog.Builder(act).setIcon(R.mipmap.ic_launcher).setTitle("Reinstall needed once")
                                .setMessage("This build is signed with a different key from the installed app, so Android can't update it in place. Uninstall Turbo Button, then install the new build from the release page. Saved reports are removed with the app, so share any you need first. Updates after that install normally.")
                                .setPositiveButton("Open release page", (d, w) -> {
                                    try { act.startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse("https://github.com/" + REPO + "/releases/tag/" + TAG))); } catch (Exception ignored) { }
                                })
                                .setNegativeButton("Cancel", null).show();
                        return;
                    }
                    install(act, out);
                });
            } catch (Exception e) {
                act.runOnUiThread(dlg::dismiss);
                toast(act, "Download failed: " + e.getMessage());
            }
        }).start();
    }

    @SuppressWarnings("deprecation")
    private static boolean sameSigner(Context c, File apk) {
        try {
            PackageManager pm = c.getPackageManager();
            Signature[] mine, theirs;
            if (Build.VERSION.SDK_INT >= 28) {
                PackageInfo a = pm.getPackageInfo(c.getPackageName(), PackageManager.GET_SIGNING_CERTIFICATES);
                PackageInfo b = pm.getPackageArchiveInfo(apk.getPath(), PackageManager.GET_SIGNING_CERTIFICATES);
                if (b == null || b.signingInfo == null) return true; // can't tell: let the installer decide
                mine = a.signingInfo.getApkContentsSigners();
                theirs = b.signingInfo.getApkContentsSigners();
            } else {
                mine = pm.getPackageInfo(c.getPackageName(), PackageManager.GET_SIGNATURES).signatures;
                PackageInfo b = pm.getPackageArchiveInfo(apk.getPath(), PackageManager.GET_SIGNATURES);
                if (b == null || b.signatures == null) return true;
                theirs = b.signatures;
            }
            return Arrays.equals(mine, theirs);
        } catch (Exception e) { return true; }
    }

    private static void install(Activity act, File f) {
        if (!act.getPackageManager().canRequestPackageInstalls()) {
            toast(act, "Allow Turbo Button to install apps, then check for updates again.");
            try { act.startActivity(new Intent(android.provider.Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + act.getPackageName()))); } catch (Exception ignored) { }
            return;
        }
        Uri uri = FileProvider.getUriForFile(act, act.getPackageName() + ".files", f);
        try {
            act.startActivity(new Intent(Intent.ACTION_VIEW).setDataAndType(uri, "application/vnd.android.package-archive")
                    .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION));
        } catch (Exception e) { toast(act, "Couldn't start the installer."); }
    }

    static void notifyAvailable(Context ctx, int build) {
        if (Build.VERSION.SDK_INT >= 33 && ctx.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) return;
        NotificationManager nm = (NotificationManager) ctx.getSystemService(Context.NOTIFICATION_SERVICE);
        nm.createNotificationChannel(new NotificationChannel("updates", "App updates", NotificationManager.IMPORTANCE_DEFAULT));
        PendingIntent pi = PendingIntent.getActivity(ctx, 2,
                new Intent(ctx, MainActivity.class).setAction(ACTION_UPDATE).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
                PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        nm.notify(43, new Notification.Builder(ctx, "updates")
                .setSmallIcon(R.drawable.ic_stat_update)
                .setLargeIcon(appIcon(ctx))
                .setColor(0xFF33FF66)
                .setContentTitle("Turbo Button update available")
                .setContentText("Build " + build + " is ready. Tap to install.")
                .setContentIntent(pi).setAutoCancel(true).build());
    }

    /** The launcher icon as a bitmap (it's an adaptive icon, so it has to be drawn). */
    private static android.graphics.Bitmap appIcon(Context ctx) {
        try {
            android.graphics.drawable.Drawable d = ctx.getPackageManager().getApplicationIcon(ctx.getPackageName());
            int px = (int) (64 * ctx.getResources().getDisplayMetrics().density);
            android.graphics.Bitmap b = android.graphics.Bitmap.createBitmap(px, px, android.graphics.Bitmap.Config.ARGB_8888);
            android.graphics.Canvas c = new android.graphics.Canvas(b);
            d.setBounds(0, 0, px, px);
            d.draw(c);
            return b;
        } catch (Exception e) { return null; }
    }

    /** (Re)schedules the twice-daily background check, or cancels it when auto-check is off. */
    static void schedule(Context c) {
        JobScheduler js = (JobScheduler) c.getSystemService(Context.JOB_SCHEDULER_SERVICE);
        if (js == null) return;
        if (!auto(c)) { js.cancel(JOB_ID); return; }
        if (js.getPendingJob(JOB_ID) != null) return;
        js.schedule(new JobInfo.Builder(JOB_ID, new ComponentName(c, CheckJob.class))
                .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
                .setPeriodic(12 * 3600_000L)
                .setPersisted(true)
                .build());
    }

    /** Background check; posts a notification once per new build. */
    public static final class CheckJob extends JobService {
        @Override
        public boolean onStartJob(JobParameters params) {
            new Thread(() -> {
                if (auto(this)) {
                    Info r = check();
                    SharedPreferences p = prefs(this);
                    if (r.error == null && r.build > current(this) && p.getInt("notified", 0) != r.build) {
                        notifyAvailable(this, r.build);
                        p.edit().putInt("notified", r.build).apply();
                    }
                }
                jobFinished(params, false);
            }).start();
            return true;
        }

        @Override
        public boolean onStopJob(JobParameters params) { return true; }
    }
}
