package chat.bounce;

import java.util.concurrent.ConcurrentHashMap;

import android.content.Context;
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
import android.service.notification.StatusBarNotification;

import go.Seq;
import goservice.Goservice;
import goservice.NotificationData;

public class GoForegroundService extends Service {

    private static final String TAG = "GoForegroundService";

    /** Intent action used to stop the service. */
    public static final String ACTION_STOP  = "chat.bounce.FOREGROUND_SERVICE_STOP";

    /** Extras that carry the user-visible notification content. */

    private static final String CHANNEL_ID   = "chat.bounce.foreground_service_channel";
    private static final String MESSAGE_CHANNEL_ID   = "chat.bounce.message_channel";
    private static final int    NOTIFICATION_ID = 1;
    private static final long   UPDATE_INTERVAL_MS = 1000; // 1 second

    private Thread serverThread;
    private Handler notificationHandler;
    private Runnable notificationUpdateRunnable;
    private int currentIconRes = android.R.drawable.ic_notification_overlay;
    private boolean isServiceRunning = false;
    private volatile boolean isNotificationPollRunning = false;

    private ConcurrentHashMap<String, Integer> notificationUUIDs = new ConcurrentHashMap<>();

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
                    Log.e(TAG, "error", e);
                }
            }, "bounce-service");
            serverThread.setDaemon(false); // keep JVM alive
            serverThread.start();
        }

        isServiceRunning = true;

        if (!isNotificationPollRunning) {
            isNotificationPollRunning = true;
            Thread newNotificationWorker = new Thread(new PostNewNotifications());
            newNotificationWorker.start();
            Thread clearNotificationWorker = new Thread(new ClearNotifications());
            clearNotificationWorker.start();
        }

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

    private class PostNewNotifications implements Runnable {
        @Override
        public void run() {
	    int id = 1;
            while (isNotificationPollRunning && !Thread.currentThread().isInterrupted()) {
                try {
                    NotificationData result = Goservice.getNotification();
                    notificationUUIDs.put(result.getID(), id);
                    showNotification(id, result.getTitle(), result.getContent());
		    id = id +1;
                } catch (Exception e) {
                    Log.e(TAG, "notification loop error", e);
                }
            }
        }
    }

    private class ClearNotifications implements Runnable {
        @Override
        public void run() {
            while (isNotificationPollRunning && !Thread.currentThread().isInterrupted()) {
                try {
                    NotificationManager notificationManager = (NotificationManager) getSystemService(Context.NOTIFICATION_SERVICE);

		    String uuid = Goservice.getNotificationToClear();
		    if (notificationUUIDs.containsKey(uuid) == true) {
                        int id = notificationUUIDs.get(uuid);
                        if (id != 0) {
                            notificationManager.cancel(id);
		        }
		    }

                    StatusBarNotification[] activeNotifications = notificationManager.getActiveNotifications();
                    if (activeNotifications.length == 1) {
			 notificationUUIDs.clear();
		    }
                } catch (Exception e) {
                    Log.e(TAG, "notification clearing loop error", e);
                }
            }
        }
    }

    public void showNotification(int id, String title, String message) {
        NotificationManager notificationManager = (NotificationManager) getSystemService(Context.NOTIFICATION_SERVICE);
        Notification.Builder builder;

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel channel = new NotificationChannel(
                MESSAGE_CHANNEL_ID,
                "App Notifications",
                NotificationManager.IMPORTANCE_HIGH
            );
            notificationManager.createNotificationChannel(channel);

            builder = new Notification.Builder(this, MESSAGE_CHANNEL_ID);
        } else {
            builder = new Notification.Builder(this);
        }
    
        Intent tapIntent = new Intent(this, org.golang.app.GoNativeActivity.class);
        tapIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
        int piFlags = Build.VERSION.SDK_INT >= Build.VERSION_CODES.M
                ? PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT
                : PendingIntent.FLAG_UPDATE_CURRENT;
        PendingIntent pendingIntent = PendingIntent.getActivity(this, 0, tapIntent, piFlags);
    
        builder.setContentTitle(title)
                .setSmallIcon(R.drawable.adaptive_icon)
                .setGroup(title)
                .setContentText(message)
                .setContentIntent(pendingIntent)
                .setSortKey(Integer.toString(id))
                .setAutoCancel(true);
    
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            builder.setPriority(Notification.PRIORITY_DEFAULT);
        }
    
        notificationManager.notify(id, builder.build());
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
