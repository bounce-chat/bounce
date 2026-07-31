package chat.bounce.notifications

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.RemoteInput
import androidx.core.content.ContextCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/**
 * Handles the inline reply and mark-as-read actions on message notifications.
 *
 * Everything it does ends in a blocking JNI call into the Go engine, so the work
 * is moved off the main thread with `goAsync()` plus a coroutine - a
 * BroadcastReceiver's `onReceive` runs on the main thread and its process may be
 * reclaimed the moment it returns, and `goAsync()` is what holds the process up
 * until the send has actually gone out.
 *
 * That hold is itself bounded by [ACTION_BUDGET_MS], because the system's
 * patience with a held-open broadcast is finite and the engine's is not.
 */
class NotificationActionReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        val action = intent.action ?: return
        val threadId = intent.getStringExtra(Conversations.EXTRA_THREAD_ID) ?: return
        val appContext = context.applicationContext

        // Read the RemoteInput here, on the main thread: the Intent is only
        // guaranteed valid for the duration of onReceive.
        val reply = if (action == ACTION_REPLY) {
            RemoteInput.getResultsFromIntent(intent)
                ?.getCharSequence(KEY_REPLY_TEXT)
                ?.toString()
                ?.trim()
                .orEmpty()
        } else {
            ""
        }

        val pending = goAsync()
        scope.launch {
            try {
                val finished = withTimeoutOrNull(ACTION_BUDGET_MS) {
                    handle(appContext, action, threadId, reply)
                    true
                }
                if (finished == null) {
                    Log.w(TAG, "notification action $action still in the engine after ${ACTION_BUDGET_MS}ms; releasing the broadcast")
                }
            } catch (t: Throwable) {
                Log.w(TAG, "notification action $action failed", t)
            } finally {
                pending.finish()
            }
        }
    }

    private suspend fun handle(
        context: Context,
        action: String,
        threadId: String,
        reply: String,
    ) {
        when (action) {
            ACTION_REPLY -> {
                if (reply.isEmpty()) return
                notifier(context)?.replyTo(threadId, reply)
            }

            ACTION_MARK_READ -> {
                // Taken down here, before anything that can block or fail. The
                // notifier below may not exist yet - if the process was reclaimed
                // while the notification sat in the shade, reaching it means
                // starting the service and waiting for the engine, and that wait
                // can time out. The user asked for this notification to go away;
                // that must not be contingent on a cold start succeeding.
                NotificationManagerCompat.from(context)
                    .cancel(BounceMessageNotifier.notificationIdFor(threadId))
                notifier(context)?.markRead(threadId)
            }

            // Swiped away. Nothing to cancel - only the in-memory history to drop,
            // so the next message does not resurrect what the user dismissed.
            ACTION_DISMISS -> BounceMessageNotifier.current?.onDismissed(threadId)

            else -> Log.w(TAG, "unknown notification action $action")
        }
    }

    /**
     * The notifier normally already exists - the service that owns it is what
     * posted the notification being acted on. It will not exist if the process
     * was reclaimed while the notification stayed in the shade, in which case the
     * service is started (a notification action grants the temporary background
     * start exemption) and we wait briefly for the engine to come up. Waiting
     * rather than failing matters: dropping a reply the user has already typed
     * and sent is much worse than a couple of seconds of latency.
     */
    private suspend fun notifier(context: Context): BounceMessageNotifier? {
        BounceMessageNotifier.current?.let { return it }

        runCatching {
            ContextCompat.startForegroundService(
                context,
                // By name rather than by class so this receiver does not depend on
                // the service's compilation unit; the manifest pins the name.
                Intent().setClassName(context, SERVICE_CLASS),
            )
        }.onFailure { Log.w(TAG, "could not start $SERVICE_CLASS", it) }

        val ready = withTimeoutOrNull(ENGINE_WAIT_MS) {
            var found = BounceMessageNotifier.current
            while (found == null) {
                delay(POLL_INTERVAL_MS)
                found = BounceMessageNotifier.current
            }
            found
        }
        if (ready == null) Log.w(TAG, "engine did not come up in time; action dropped")
        return ready
    }

    companion object {
        const val ACTION_REPLY = "chat.bounce.action.REPLY"
        const val ACTION_MARK_READ = "chat.bounce.action.MARK_READ"
        const val ACTION_DISMISS = "chat.bounce.action.DISMISS"

        /** RemoteInput result key carrying the typed reply. */
        const val KEY_REPLY_TEXT = "chat.bounce.extra.REPLY_TEXT"

        private const val SERVICE_CLASS = "chat.bounce.service.BounceService"
        private const val TAG = "BounceNotifAction"

        /**
         * A broadcast held open with goAsync() has roughly ten seconds before the
         * system considers the receiver hung; the engine has to start *and* the
         * message has to be sent inside that, so the wait is kept well short of it.
         */
        private const val ENGINE_WAIT_MS = 6_000L
        private const val POLL_INTERVAL_MS = 100L

        /**
         * Hard ceiling on how long the broadcast is held, covering the engine
         * wait *and* the call itself. Overrunning the system's budget is not a
         * slow action, it is a dead process: the engine serialises its database
         * on one connection, so a mark-read that lands during a busy write has
         * been observed blocking for ~12s, and the system responded by killing
         * and restarting the app.
         *
         * Note this cannot abort the call - a bound engine call is a blocking
         * JNI hop and coroutine cancellation cannot interrupt it. The thread
         * stays in Go until it returns, and the action still takes effect
         * whenever that is. What the timeout buys is that *we* stop waiting on
         * it, so the broadcast finishes inside its budget. That makes the
         * failure mode "the action lands late" instead of "the process dies",
         * which also means it is safe for REPLY: a reply that overruns is still
         * sent, just not confirmed within the window.
         */
        private const val ACTION_BUDGET_MS = 8_000L

        /**
         * Process-scoped: receiver instances are created per broadcast and thrown
         * away, so the scope cannot live on the instance.
         */
        private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    }
}
