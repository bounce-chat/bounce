package chat.bounce;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Intent;
import android.os.Build;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.util.Log;

import go.Seq;
import goservice.Goservice;

public class GoForegroundService extends Service {

    private static final String TAG = "GoForegroundService";

    /** Intent action used to stop the service. */
    public static final String ACTION_STOP  = "chat.bounce.FOREGROUND_SERVICE_STOP";

    /** Extras that carry the user-visible notification content. */

    private static final String CHANNEL_ID   = "chat.bounce.foreground_service_channel";
    private static final int    NOTIFICATION_ID = 1;
    private static final long   UPDATE_INTERVAL_MS = 1000; // 1 second

    private Thread serverThread;
    private Handler notificationHandler;
    private Runnable notificationUpdateRunnable;
    private String currentContent = "0s";
    private int currentIconRes = android.R.drawable.ic_notification_overlay;
    private boolean isServiceRunning = false;

    // -----------------------------------------------------------------------
    // AIDL Binder — runs methods in the service process (safe for Go runtime B)
    // -----------------------------------------------------------------------

    private final IGoService.Stub binder = new IGoService.Stub() {
        @Override
        public String getEvents() {
            return Goservice.getEvents();
        }
        @Override
        public String eval(String arg) {
            return Goservice.eval(arg);
        }
    };

    // -----------------------------------------------------------------------
    // Service lifecycle
    // -----------------------------------------------------------------------

    @Override
    public void onCreate() {
        super.onCreate();
        createNotificationChannel();
        notificationHandler = new Handler(Looper.getMainLooper());
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent == null || ACTION_STOP.equals(intent.getAction())) {
            Log.d(TAG, "Stopping foreground service");
            stopForeground(true);
            stopSelf();
            return START_NOT_STICKY;
        }

        Log.d(TAG, "Starting foreground service");

        // Build a tap intent that brings the main activity back to front.
        Intent tapIntent = new Intent(this, org.golang.app.GoNativeActivity.class);
        tapIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
        int piFlags = Build.VERSION.SDK_INT >= Build.VERSION_CODES.M
                ? PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT
                : PendingIntent.FLAG_UPDATE_CURRENT;
        PendingIntent pendingIntent = PendingIntent.getActivity(this, 0, tapIntent, piFlags);

        Notification.Builder builder;
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            builder = new Notification.Builder(this, CHANNEL_ID);
        } else {
            //noinspection deprecation
            builder = new Notification.Builder(this);
        }

        Notification notification = builder
                .setContentText(currentContent)
                .setContentIntent(pendingIntent)
                .setOngoing(true) // Makes notification non-dismissible
                .setAutoCancel(false) // Prevents dismissal on tap
                .build();

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) { // API 34
            startForeground(NOTIFICATION_ID, notification,
                    android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        } else {
            startForeground(NOTIFICATION_ID, notification);
        }

        // Start periodic notification updates
        startNotificationUpdates();

        // Prevent re-starting the server if already running
        if (serverThread == null || !serverThread.isAlive()) {
            serverThread = new Thread(() -> {
                try {
                    Seq.setContext(getApplicationContext());
                    Goservice.startForegroundService(); // must BLOCK here (e.g. http.ListenAndServe)
                } catch (Exception e) {
                    Log.e(TAG, "Go server error", e);
                }
            }, "go-http-server");
            serverThread.setDaemon(false); // keep JVM alive
            serverThread.start();
        }

        isServiceRunning = true;
        return START_STICKY;
    }

    @Override
    public IBinder onBind(Intent intent) {
        return binder;
    }

    @Override
    public void onDestroy() {
        super.onDestroy();
        // Signal Go side to shut down, then interrupt
        Seq.setContext(getApplicationContext());
        try {
            Goservice.stopForegroundService();
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
        stopNotificationUpdates();
        if (serverThread != null) {
            serverThread.interrupt();
        }
    }


    // -----------------------------------------------------------------------
    // Notification management
    // -----------------------------------------------------------------------

    private Notification buildNotification() {
        // Build a tap intent that brings the main activity back to front.
        Intent tapIntent = new Intent(this, org.golang.app.GoNativeActivity.class);
        tapIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
        int piFlags = Build.VERSION.SDK_INT >= Build.VERSION_CODES.M
                ? PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT
                : PendingIntent.FLAG_UPDATE_CURRENT;
        PendingIntent pendingIntent = PendingIntent.getActivity(this, 0, tapIntent, piFlags);

        // Build close intent
        Intent closeIntent = new Intent(this, GoForegroundService.class);
        closeIntent.setAction(ACTION_STOP);
        PendingIntent closePendingIntent = PendingIntent.getService(this, 1, closeIntent, piFlags);

        Notification.Builder builder;
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            builder = new Notification.Builder(this, CHANNEL_ID);
        } else {
            //noinspection deprecation
            builder = new Notification.Builder(this);
        }

        //currentContent = Goservice.getNotificationContent();

        Notification.Builder dest = builder
                .setContentText("Bounce is running in the background")
                .setSmallIcon(currentIconRes)
                .setContentIntent(pendingIntent)
                .setOngoing(true) // Makes notification non-dismissible
                .setAutoCancel(false); // Prevents dismissal on tap

        dest.addAction(android.R.drawable.ic_menu_close_clear_cancel, "Quit", closePendingIntent);


        return dest.build();
    }

    private void startNotificationUpdates() {
        if (notificationUpdateRunnable != null) {
            notificationHandler.removeCallbacks(notificationUpdateRunnable);
        }

        notificationUpdateRunnable = new Runnable() {
            @Override
            public void run() {
                if (isServiceRunning) {
                    updateNotification();
                    notificationHandler.postDelayed(this, UPDATE_INTERVAL_MS);
                }
            }
        };

        notificationHandler.postDelayed(notificationUpdateRunnable, UPDATE_INTERVAL_MS);
    }

    private void stopNotificationUpdates() {
        if (notificationUpdateRunnable != null && notificationHandler != null) {
            notificationHandler.removeCallbacks(notificationUpdateRunnable);
            notificationUpdateRunnable = null;
        }
    }

    private void updateNotification() {
        NotificationManager manager = getSystemService(NotificationManager.class);
        if (manager != null) {
            Notification notification = buildNotification();
            manager.notify(NOTIFICATION_ID, notification);
        }
    }

    // -----------------------------------------------------------------------
    // Private helpers
    // -----------------------------------------------------------------------

    private void createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel channel = new NotificationChannel(
                    CHANNEL_ID,
                    "Background service",
                    NotificationManager.IMPORTANCE_LOW);
            channel.setDescription("Keeps the app running in the background");
            NotificationManager manager = getSystemService(NotificationManager.class);
            if (manager != null) {
                manager.createNotificationChannel(channel);
            }
        }
    }
}
