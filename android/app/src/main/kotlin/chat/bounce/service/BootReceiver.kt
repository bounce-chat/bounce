package chat.bounce.service

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/**
 * Brings the engine back after a reboot or an app update.
 *
 * Bounce has no push channel - there is no server to send one - so a device
 * that reboots and never restarts the service simply stops receiving messages
 * until the user opens the app. That is the whole reason this receiver exists.
 *
 * There is deliberately no "is there a profile yet?" guard: answering that
 * requires the engine, and starting the engine is exactly what this receiver is
 * trying to do. [BounceService] starts un-provisioned engines happily, so the
 * decision is left to it.
 *
 * Receiving either broadcast also creates the process, which runs
 * BounceApplication.onCreate, which starts the service too. The explicit start
 * here is redundant in that case and harmless - a second startForegroundService
 * on a running service is just another onStartCommand - but it keeps the
 * behaviour correct if the process was already alive.
 */
class BootReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent?) {
        when (intent?.action) {
            Intent.ACTION_BOOT_COMPLETED,
            Intent.ACTION_MY_PACKAGE_REPLACED,
            -> BounceService.start(context)
        }
    }
}
