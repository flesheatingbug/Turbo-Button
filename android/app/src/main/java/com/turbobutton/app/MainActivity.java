package com.turbobutton.app;

import android.app.Activity;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.webkit.JavascriptInterface;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Toast;

import androidx.core.content.FileProvider;
import androidx.webkit.WebViewAssetLoader;

import com.google.mlkit.vision.barcode.common.Barcode;
import com.google.mlkit.vision.codescanner.GmsBarcodeScanner;
import com.google.mlkit.vision.codescanner.GmsBarcodeScannerOptions;
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Iterator;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.regex.Pattern;

/**
 * Hosts the Turbo Button control page (bundled in the APK) in a WebView and gives it
 * a small native bridge: HTTP without browser CORS limits, encrypted settings,
 * the QR scanner and saved reports.
 */
public class MainActivity extends Activity {
    private static final String HOST = "appassets.androidplatform.net";
    private static final Pattern REPORT_NAME = Pattern.compile("^[A-Za-z0-9_.\\-]{1,140}\\.html$");

    private WebView web;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final ExecutorService pool = Executors.newCachedThreadPool();
    private SecureStore store;
    private File reportsDir;
    private volatile String sharedIn; // text shared into the app (an AI reply), until the page takes it

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        store = new SecureStore(this);
        takeIntent(getIntent());
        reportsDir = new File(getFilesDir(), "reports");
        //noinspection ResultOfMethodCallIgnored
        reportsDir.mkdirs();

        if ((getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0) {
            WebView.setWebContentsDebuggingEnabled(true);
        }

        web = new WebView(this);
        setContentView(web);

        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setAllowFileAccess(false);
        s.setAllowContentAccess(false);
        // The page ships inside the APK: never show a cached copy from an older install.
        s.setCacheMode(WebSettings.LOAD_NO_CACHE);
        web.clearCache(true);

        final WebViewAssetLoader loader = new WebViewAssetLoader.Builder()
                .setDomain(HOST)
                .addPathHandler("/assets/", new WebViewAssetLoader.AssetsPathHandler(this))
                .build();

        web.setWebViewClient(new WebViewClient() {
            @Override
            public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
                return loader.shouldInterceptRequest(request.getUrl());
            }

            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                Uri u = request.getUrl();
                if (HOST.equals(u.getHost())) return false;
                // Never navigate the bridge-enabled WebView elsewhere; open links outside.
                try { startActivity(new Intent(Intent.ACTION_VIEW, u)); } catch (Exception ignored) { }
                return true;
            }
        });
        web.setWebChromeClient(new WebChromeClient()); // alert()/confirm() dialogs
        web.addJavascriptInterface(new Bridge(), "TurboNative");
        web.loadUrl("https://" + HOST + "/assets/index.html");
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        if (takeIntent(intent) && web != null) {
            web.evaluateJavascript("window.__sharedIn && window.__sharedIn()", null);
        }
    }

    private boolean takeIntent(Intent intent) {
        if (intent == null || !Intent.ACTION_SEND.equals(intent.getAction())) return false;
        String t = intent.getStringExtra(Intent.EXTRA_TEXT);
        if (t == null || t.isEmpty()) return false;
        sharedIn = t;
        return true;
    }

    @Override
    protected void onDestroy() {
        pool.shutdownNow();
        if (web != null) {
            web.removeJavascriptInterface("TurboNative");
            web.destroy();
            web = null;
        }
        super.onDestroy();
    }

    private void resolve(String id, boolean ok, String payload) {
        final String js = "window.__nr(" + JSONObject.quote(id) + "," + ok + "," + JSONObject.quote(payload == null ? "" : payload) + ")";
        main.post(() -> {
            if (web != null) web.evaluateJavascript(js, null);
        });
    }

    private void toast(String msg) {
        main.post(() -> Toast.makeText(this, msg, Toast.LENGTH_LONG).show());
    }

    private File reportFile(String name) {
        if (name == null || !REPORT_NAME.matcher(name).matches()) throw new IllegalArgumentException("bad report name");
        return new File(reportsDir, name);
    }

    private static String readAll(InputStream in) throws IOException {
        ByteArrayOutputStream bo = new ByteArrayOutputStream();
        byte[] buf = new byte[16384];
        int n;
        while ((n = in.read(buf)) > 0) bo.write(buf, 0, n);
        in.close();
        return new String(bo.toByteArray(), StandardCharsets.UTF_8);
    }

    private String doRequest(JSONObject r) throws Exception {
        URL url = new URL(r.getString("url"));
        String proto = url.getProtocol();
        if (!"http".equals(proto) && !"https".equals(proto)) throw new IOException("only http/https allowed");
        HttpURLConnection c = (HttpURLConnection) url.openConnection();
        try {
            int timeout = r.optInt("timeoutMs", 120000);
            c.setConnectTimeout(Math.min(timeout, 15000));
            c.setReadTimeout(timeout);
            String method = r.optString("method", "GET");
            c.setRequestMethod(method);
            JSONObject headers = r.optJSONObject("headers");
            if (headers != null) {
                for (Iterator<String> it = headers.keys(); it.hasNext(); ) {
                    String k = it.next();
                    c.setRequestProperty(k, headers.getString(k));
                }
            }
            Object body = r.opt("body");
            if (body instanceof String && !"GET".equals(method)) {
                byte[] bytes = ((String) body).getBytes(StandardCharsets.UTF_8);
                c.setDoOutput(true);
                c.setFixedLengthStreamingMode(bytes.length);
                try (OutputStream os = c.getOutputStream()) { os.write(bytes); }
            }
            int code = c.getResponseCode();
            InputStream is = code >= 400 ? c.getErrorStream() : c.getInputStream();
            String text = is == null ? "" : readAll(is);
            JSONObject out = new JSONObject();
            out.put("status", code);
            out.put("body", text);
            return out.toString();
        } finally {
            c.disconnect();
        }
    }

    /** Methods callable from the page as window.TurboNative.*. They run on a WebView background thread. */
    final class Bridge {
        @JavascriptInterface
        public void request(final String id, final String json) {
            pool.execute(() -> {
                try {
                    resolve(id, true, doRequest(new JSONObject(json)));
                } catch (Exception e) {
                    resolve(id, false, e.getClass().getSimpleName() + ": " + e.getMessage());
                }
            });
        }

        @JavascriptInterface
        public void copyText(final String text) {
            main.post(() -> {
                try {
                    ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
                    cm.setPrimaryClip(ClipData.newPlainText("Turbo Button", text));
                } catch (Exception e) {
                    toast("Could not copy: " + e.getMessage());
                }
            });
        }

        /** Returns (once) text that was shared into the app, e.g. an AI app's reply. */
        @JavascriptInterface
        public String takeShared() {
            String t = sharedIn;
            sharedIn = null;
            return t == null ? "" : t;
        }

        /**
         * Opens the share sheet with the scan as a .txt attachment and the short instruction as the message,
         * so AI apps treat the instruction as the user's request and the file as data. The instruction is
         * also put on the clipboard in case the chosen app doesn't fill in the message box.
         */
        @JavascriptInterface
        public void shareToAI(final String fileName, final String content, final String message) {
            main.post(() -> {
                try {
                    if (fileName == null || !fileName.matches("^[A-Za-z0-9_.\\-]{1,80}\\.txt$")) throw new IllegalArgumentException("bad file name");
                    File dir = new File(getCacheDir(), "share");
                    //noinspection ResultOfMethodCallIgnored
                    dir.mkdirs();
                    File f = new File(dir, fileName);
                    try (FileOutputStream fo = new FileOutputStream(f)) { fo.write(content.getBytes(StandardCharsets.UTF_8)); }
                    Uri uri = FileProvider.getUriForFile(MainActivity.this, getPackageName() + ".files", f);
                    ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
                    cm.setPrimaryClip(ClipData.newPlainText("Turbo Button", message));
                    Intent send = new Intent(Intent.ACTION_SEND)
                            .setType("text/plain")
                            .putExtra(Intent.EXTRA_STREAM, uri)
                            .putExtra(Intent.EXTRA_TEXT, message)
                            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
                    startActivity(Intent.createChooser(send, "Send to your AI app"));
                } catch (Exception e) {
                    toast("Could not share: " + e.getMessage());
                }
            });
        }

        @JavascriptInterface
        public String getSettings() {
            return store.get("ai");
        }

        @JavascriptInterface
        public void saveSettings(String json) {
            try { store.put("ai", json); } catch (Exception e) { toast(e.getMessage()); }
        }

        @JavascriptInterface
        public void scanQR(final String id) {
            main.post(() -> {
                try {
                    GmsBarcodeScannerOptions opts = new GmsBarcodeScannerOptions.Builder()
                            .setBarcodeFormats(Barcode.FORMAT_QR_CODE)
                            .build();
                    GmsBarcodeScanner scanner = GmsBarcodeScanning.getClient(MainActivity.this, opts);
                    scanner.startScan()
                            .addOnSuccessListener(code -> resolve(id, true, code.getRawValue()))
                            .addOnCanceledListener(() -> resolve(id, false, "cancelled"))
                            .addOnFailureListener(e -> resolve(id, false, String.valueOf(e.getMessage())));
                } catch (Exception e) {
                    resolve(id, false, String.valueOf(e.getMessage()));
                }
            });
        }

        @JavascriptInterface
        public void saveReport(String name, String html) {
            try (FileOutputStream fo = new FileOutputStream(reportFile(name))) {
                fo.write(html.getBytes(StandardCharsets.UTF_8));
            } catch (Exception e) {
                toast("Could not save report: " + e.getMessage());
            }
        }

        @JavascriptInterface
        public String listReports() {
            File[] files = reportsDir.listFiles((d, n) -> REPORT_NAME.matcher(n).matches());
            JSONArray arr = new JSONArray();
            if (files == null) return arr.toString();
            Arrays.sort(files, (a, b) -> Long.compare(b.lastModified(), a.lastModified()));
            try {
                for (File f : files) {
                    JSONObject o = new JSONObject();
                    o.put("name", f.getName());
                    o.put("size", f.length());
                    o.put("modified", f.lastModified());
                    arr.put(o);
                }
            } catch (Exception ignored) { }
            return arr.toString();
        }

        @JavascriptInterface
        public String readReport(String name) {
            try (FileInputStream in = new FileInputStream(reportFile(name))) {
                return readAll(in);
            } catch (Exception e) {
                return "";
            }
        }

        @JavascriptInterface
        public void deleteReport(String name) {
            try {
                //noinspection ResultOfMethodCallIgnored
                reportFile(name).delete();
            } catch (Exception ignored) { }
        }

        @JavascriptInterface
        public void shareReport(final String name) {
            main.post(() -> {
                try {
                    File f = reportFile(name);
                    Uri uri = FileProvider.getUriForFile(MainActivity.this, getPackageName() + ".files", f);
                    Intent send = new Intent(Intent.ACTION_SEND)
                            .setType("text/html")
                            .putExtra(Intent.EXTRA_STREAM, uri)
                            .putExtra(Intent.EXTRA_SUBJECT, name.replace(".html", ""))
                            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
                    startActivity(Intent.createChooser(send, "Share report"));
                } catch (Exception e) {
                    toast("Could not share: " + e.getMessage());
                }
            });
        }
    }
}
