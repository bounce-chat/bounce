package chat.bounce.service

import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.LifecycleService
import androidx.lifecycle.lifecycleScope
import chat.bounce.BounceApplication
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.data.EngineEventPump
import chat.bounce.engine.EngineHolder
import chat.bounce.notifications.BounceMessageNotifier
import chat.bounce.notifications.NotificationChannels
import chat.bounce.ui.MainActivity
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.launch

/**
 * Lifetime anchor for the Go engine.
 *
 * This service is NOT an IPC endpoint and has no binder API - the UI runs in
 * the same process and calls the engine directly through [EngineHolder]. Its
 * only jobs are to (a) hold a foreground lifetime so Android leaves the process
 * alive while Tor keeps the hidden service reachable, and (b) own the one
 * allowed consumer of the engine signal stream.
 *
 * Bounce is serverless: there is no push service that can wake the app when a
 * message arrives, because there is no server to send the push. If the process
 * dies, messages are simply not received. That is what the specialUse
 * foreground service type is declared for in the manifest.
 */
class BounceService : LifecycleService() {

    private enum class Phase { CONNECTING, ONLINE, OFFLINE, STOPPING }

    private val phase = MutableStateFlow(Phase.CONNECTING)

    override fun onCreate() {
        super.onCreate()

        // Android gives us ~10s from startForegroundService() to this call and
        // kills the process with an ANR otherwise, so it happens before any
        // suspension point and long before the engine is up.
        if (!enterForeground(Phase.CONNECTING)) {
            stopSelf()
            return
        }

        lifecycleScope.launch {
            // drop(1): onCreate already posted the CONNECTING notification.
            phase.drop(1).collect { enterForeground(it) }
        }

        lifecycleScope.launch { boot() }
        lifecycleScope.launch { keepNotificationPresent() }
    }

    /**
     * Backstop for a dismissed foreground notification.
     *
     * From Android 14 the user may swipe away a foreground service's
     * notification. The service keeps running, but Bounce has no server and no
     * push channel, so a user who dismisses it has no way to tell the app is
     * still live - and no way to get back to it. [ACTION_RESTORE] re-posts
     * immediately via the delete intent, which covers the ordinary case; this
     * loop catches the ones that arrive without one, such as the shade being
     * cleared wholesale.
     *
     * The Fyne service re-posted every second. Once a second is unnecessary
     * when the delete intent already handles dismissal instantly, and this is a
     * battery-sensitive app, so the sweep is slower. delay() does not hold a
     * wakelock, so a sleeping device simply stops checking until it wakes.
     */
    private suspend fun keepNotificationPresent() {
        while (true) {
            delay(NOTIFICATION_SWEEP_MS)
            if (phase.value != Phase.STOPPING && !notificationPresent()) {
                Log.i(TAG, "foreground notification vanished, re-posting")
                enterForeground(phase.value)
            }
        }
    }

    private fun notificationPresent(): Boolean {
        val manager = getSystemService(NotificationManager::class.java) ?: return true
        return manager.activeNotifications.any { it.id == FOREGROUND_NOTIFICATION_ID }
    }

    /**
     * Engine start -> repository snapshot -> event pump, in that order.
     *
     * The bridge channel is UNLIMITED, so every signal the engine emits while
     * the snapshot is being taken is buffered and replayed to the pump in
     * order; nothing is lost by starting the pump last. Starting it *first*
     * would be the actual hazard, since [ChatRepository.load] installs a
     * whole-state snapshot and would clobber any delta the pump had already
     * applied on top of it. Replay is safe because the repository keys
     * everything by UUID, so re-applying a signal already reflected in the
     * snapshot is idempotent.
     */
    private suspend fun boot() {
        val client = try {
            EngineHolder.start(this).also { ChatRepository.load(it) }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Throwable) {
            // A first run with no profile yet is not an error - the engine
            // starts un-ready and the onboarding UI drives it - so anything
            // caught here is a genuine failure. Stay in the foreground so the
            // user can see the offline state and retry from the UI rather than
            // silently losing the process.
            Log.e(TAG, "engine failed to start", e)
            phase.value = Phase.OFFLINE
            return
        }

        val bridge = EngineHolder.bridge
        if (bridge == null) {
            Log.e(TAG, "engine started without a bridge; no signals will be delivered")
            phase.value = Phase.OFFLINE
            return
        }

        lifecycleScope.launch {
            ChatRepository.networkOnline.collect { online ->
                phase.value = if (online) Phase.ONLINE else Phase.OFFLINE
            }
        }

        val notifier = BounceMessageNotifier(applicationContext, client, ChatRepository)
        EngineEventPump(client, bridge, ChatRepository, notifier).start(lifecycleScope)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        super.onStartCommand(intent, flags, startId)

        when (intent?.action) {
            ACTION_STOP -> {
                // The engine is torn down in onDestroy, which covers this path
                // as well as being killed by the system, so there is one
                // shutdown path. STOPPING stops the sweep from resurrecting the
                // notification on the way out.
                phase.value = Phase.STOPPING
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf()
                return START_NOT_STICKY
            }

            // Fired by the notification's delete intent: the user swiped it
            // away, which Android 14 permits for a foreground service. Put it
            // straight back.
            ACTION_RESTORE -> {
                if (phase.value != Phase.STOPPING) enterForeground(phase.value)
                return START_STICKY
            }
        }

        // Sticky so the system brings the process back after a low-memory kill;
        // a restarted service re-runs onCreate and re-starts the engine.
        return START_STICKY
    }

    /**
     * Swiping the app off the recents list must not take the engine with it.
     * Bounce receives messages only while this process is alive, so the service
     * outlives the UI deliberately.
     */
    override fun onTaskRemoved(rootIntent: Intent?) {
        super.onTaskRemoved(rootIntent)
        if (phase.value == Phase.STOPPING) return
        ContextCompat.startForegroundService(this, Intent(this, BounceService::class.java))
    }

    override fun onDestroy() {
        // On BounceApplication.scope rather than lifecycleScope: the latter is
        // cancelled as part of this very callback, so the stop would never run.
        BounceApplication.scope.launch {
            EngineHolder.stop()
        }
        super.onDestroy()
    }

    /** Returns false if the platform refused to put us in the foreground. */
    private fun enterForeground(state: Phase): Boolean = try {
        val notification = buildNotification(state)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(
                FOREGROUND_NOTIFICATION_ID,
                notification,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE,
            )
        } else {
            startForeground(FOREGROUND_NOTIFICATION_ID, notification)
        }
        true
    } catch (e: IllegalStateException) {
        // API 31+ ForegroundServiceStartNotAllowedException extends
        // IllegalStateException; catching the concrete type would put an
        // API-31 class in the exception table on API 29/30 devices.
        Log.w(TAG, "not allowed to enter the foreground", e)
        false
    } catch (e: SecurityException) {
        // API 34+: the declared FGS type does not match the held permission.
        Log.e(TAG, "foreground service type rejected", e)
        false
    }

    private fun buildNotification(state: Phase): Notification {
        val text = getString(
            when (state) {
                Phase.CONNECTING -> R.string.service_notification_connecting
                Phase.ONLINE -> R.string.service_notification_online
                Phase.OFFLINE, Phase.STOPPING -> R.string.service_notification_offline
            },
        )

        val open = Intent(this, MainActivity::class.java)
            .setFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        val openPending = PendingIntent.getActivity(
            this,
            REQUEST_OPEN,
            open,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

        val stopPending = PendingIntent.getService(
            this,
            REQUEST_STOP,
            Intent(this, BounceService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

        // Android 14 lets the user dismiss a foreground service notification
        // even with setOngoing(true). This fires when they do, so it can go
        // straight back up.
        val restorePending = PendingIntent.getService(
            this,
            REQUEST_RESTORE,
            Intent(this, BounceService::class.java).setAction(ACTION_RESTORE),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

        return NotificationCompat.Builder(this, NotificationChannels.SERVICE_CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(getString(R.string.service_notification_title))
            .setContentText(text)
            .setContentIntent(openPending)
            .setDeleteIntent(restorePending)
            .addAction(0, getString(R.string.service_stop), stopPending)
            .setOngoing(true)
            .setAutoCancel(false)
            .setSilent(true)
            .setShowWhen(false)
            .setLocalOnly(true)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    companion object {
        private const val TAG = "BounceService"

        /**
         * Must match the id [chat.bounce.notifications.NotificationChannels]
         * creates for the low-importance "Running" channel.
         */
        // The channel itself is declared in NotificationChannels, which is the
        // only place that creates them. A Builder pointed at an id that was
        // never created posts nothing on API 26+, which for a foreground
        // service means startForeground fails and the process is killed.

        /** Reserved; message notifications must not reuse this id. */
        const val FOREGROUND_NOTIFICATION_ID = 1

        const val ACTION_STOP = "chat.bounce.action.STOP_SERVICE"

        /** Delete intent: the user dismissed the ongoing notification. */
        const val ACTION_RESTORE = "chat.bounce.action.RESTORE_NOTIFICATION"

        private const val REQUEST_OPEN = 0
        private const val REQUEST_STOP = 1
        private const val REQUEST_RESTORE = 2

        /**
         * How often to confirm the ongoing notification is still up. The delete
         * intent handles a deliberate dismissal instantly, so this only has to
         * catch the cases that do not produce one.
         */
        private const val NOTIFICATION_SWEEP_MS = 5_000L

        /**
         * Safe to call from anywhere, including a background process start.
         *
         * On API 31+ the platform throws rather than starting a foreground
         * service the app is not currently allowed to start. Android 14+ does
         * permit a specialUse FGS from a BOOT_COMPLETED receiver, but that is
         * an exemption, not a guarantee: an app that the user has "force
         * stopped", or one denied the exemption on a vendor build, will land
         * here. Losing the service is recoverable - the user opens the app and
         * the Activity's own start brings it back - so this logs rather than
         * crashing the process.
         */
        fun start(context: Context) {
            val intent = Intent(context, BounceService::class.java)
            try {
                ContextCompat.startForegroundService(context, intent)
            } catch (e: IllegalStateException) {
                Log.w(TAG, "foreground service start refused", e)
            }
        }

        fun stop(context: Context) {
            val intent = Intent(context, BounceService::class.java).setAction(ACTION_STOP)
            try {
                ContextCompat.startForegroundService(context, intent)
            } catch (e: IllegalStateException) {
                // Nothing to stop if we were not allowed to start it.
                Log.w(TAG, "foreground service stop refused", e)
            }
        }
    }
}
