package com.turbobutton.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;

/**
 * Keeps the app alive (with a quiet "working" notification) while jobs run on a PC, so the phone
 * can still notify the technician when they finish with the screen off. Stops when work is done.
 */
public class WorkService extends Service {
    static final String CH_WORK = "work", CH_JOBS = "jobs";

    static void channels(Context c) {
        NotificationManager nm = (NotificationManager) c.getSystemService(Context.NOTIFICATION_SERVICE);
        nm.createNotificationChannel(new NotificationChannel(CH_WORK, "Work in progress", NotificationManager.IMPORTANCE_LOW));
        nm.createNotificationChannel(new NotificationChannel(CH_JOBS, "Scans and fixes finished", NotificationManager.IMPORTANCE_HIGH));
    }

    static PendingIntent open(Context c) {
        return PendingIntent.getActivity(c, 1, new Intent(c, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
                PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
    }

    static void start(Context c, String text) {
        Intent i = new Intent(c, WorkService.class).putExtra("text", text);
        try { c.startForegroundService(i); } catch (Exception ignored) { }
    }

    static void stop(Context c) { c.stopService(new Intent(c, WorkService.class)); }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        channels(this);
        String text = intent != null && intent.getStringExtra("text") != null ? intent.getStringExtra("text") : "Working on a PC";
        Notification n = new Notification.Builder(this, CH_WORK)
                .setSmallIcon(R.drawable.ic_stat_update)
                .setContentTitle("Turbo Button")
                .setContentText(text)
                .setContentIntent(open(this))
                .setOngoing(true)
                .build();
        try {
            if (Build.VERSION.SDK_INT >= 34) startForeground(44, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
            else startForeground(44, n);
        } catch (Exception e) { stopSelf(); }
        return START_NOT_STICKY;
    }

    @Override
    public IBinder onBind(Intent intent) { return null; }
}
