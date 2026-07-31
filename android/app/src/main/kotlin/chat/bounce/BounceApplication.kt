package chat.bounce

import android.app.Application
import chat.bounce.notifications.NotificationChannels
import chat.bounce.service.BounceService
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

/**
 * Process entry point.
 *
 * Bounce is single-process (see AndroidManifest): the Activity, the foreground
 * service and the Go engine all live here, so this class runs exactly once per
 * process and is the right place to establish the two things everything else
 * assumes exist - the notification channels and a running [BounceService].
 */
class BounceApplication : Application() {

    /**
     * Scope for work that must outlive whichever component started it.
     *
     * The motivating case is engine shutdown: [BounceService] tears the engine
     * down from onDestroy, by which point its own lifecycleScope has already
     * been cancelled, so the stop would never run if it were launched there.
     *
     * SupervisorJob because these jobs are unrelated to one another - a failed
     * engine shutdown must not cancel unrelated background work - and
     * Dispatchers.Default because nothing launched here should assume the main
     * thread; the engine facade switches to IO on its own.
     */
    val applicationScope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    override fun onCreate() {
        super.onCreate()
        current = this

        // Must precede the service start: startForeground() posts to the
        // service channel synchronously and a missing channel is fatal.
        NotificationChannels.ensure(this)

        // The process can also be created in the background (boot broadcast,
        // FileProvider access), where starting a foreground service is not
        // permitted; BounceService.start swallows that case rather than
        // crashing the process on launch.
        BounceService.start(this)
    }

    companion object {
        // Not a leak: the Application instance is alive for as long as the
        // process is, which is exactly the lifetime of this field.
        private lateinit var current: BounceApplication

        /** Reach-through to [applicationScope] for components with no Application handle. */
        val scope: CoroutineScope get() = current.applicationScope
    }
}
