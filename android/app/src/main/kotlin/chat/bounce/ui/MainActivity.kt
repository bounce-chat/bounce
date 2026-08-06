package chat.bounce.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.core.content.ContextCompat
import androidx.core.content.IntentCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import chat.bounce.data.PendingShare
import chat.bounce.data.SharePayload
import chat.bounce.engine.EngineHolder
import chat.bounce.ui.theme.BounceTheme
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * The only Activity.
 *
 * It owns three things the Compose tree cannot: runtime permissions, the
 * notification/shortcut deep link, and the engine's idea of whether the user is
 * looking at the app.
 */
class MainActivity : ComponentActivity() {

    /** Set by an incoming intent, cleared once the nav host has acted on it. */
    private val pendingThreadId = MutableStateFlow<String?>(null)

    private val cameraPermission = PermissionState {
        cameraLauncher.launch(Manifest.permission.CAMERA)
    }

    // Explicitly typed: the launcher's callback writes back to [cameraPermission],
    // whose own initialiser launches it, so inference would chase its own tail.
    private val cameraLauncher: ActivityResultLauncher<String> =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted: Boolean ->
            cameraPermission.granted = granted
        }

    private val notificationLauncher: ActivityResultLauncher<String> =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { _: Boolean ->
            /* advisory */
        }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)

        handleIntent(intent)
        requestNotificationPermission()
        driveEngineForegroundState()

        setContent {
            BounceTheme {
                CompositionLocalProvider(LocalCameraPermission provides cameraPermission) {
                    val navController = rememberNavController()
                    val deepLink by pendingThreadId.collectAsStateWithLifecycle()
                    val entry by navController.currentBackStackEntryAsState()

                    // The engine suppresses notifications for whatever thread is
                    // open, so anything that isn't a conversation has to say so
                    // or the user stops being told about the last chat they read.
                    LaunchedEffect(entry?.destination?.route) {
                        if (entry?.destination?.route != Routes.CONVERSATION) {
                            EngineHolder.client?.setActiveThread("")
                        }
                    }

                    BounceNavHost(
                        navController = navController,
                        deepLinkThreadId = deepLink,
                        onDeepLinkHandled = { pendingThreadId.value = null },
                    )
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        // launchMode is singleTask, so a notification tap re-uses this instance;
        // without replacing the stored intent, getIntent() keeps returning the
        // launcher intent forever.
        setIntent(intent)
        handleIntent(intent)
    }

    override fun onResume() {
        super.onResume()
        cameraPermission.granted = isGranted(Manifest.permission.CAMERA)
    }

    private fun handleIntent(intent: Intent?) {
        if (intent == null) return
        if (handleShare(intent)) return

        val threadId = intent.getStringExtra(EXTRA_THREAD_ID)?.takeIf { it.isNotBlank() } ?: return
        pendingThreadId.value = threadId
        // The intent outlives the navigation; leaving the extra in place would
        // re-open the thread after every rotation.
        intent.removeExtra(EXTRA_THREAD_ID)
    }

    /**
     * Accepts an ACTION_SEND / ACTION_SEND_MULTIPLE share from another app.
     *
     * A direct-share tap names its conversation in EXTRA_SHORTCUT_ID - the
     * shortcut id is the thread UUID, so it needs no translation. A plain "share
     * to Bounce" leaves it null and the nav host asks the user to pick.
     *
     * The extras are stripped once read for the same reason the deep link is:
     * this Activity is singleTask, so the same intent is redelivered after every
     * configuration change and would otherwise re-share on each rotation.
     *
     * @return true when the intent was a share, handled or not.
     */
    private fun handleShare(intent: Intent): Boolean {
        val uris = when (intent.action) {
            Intent.ACTION_SEND ->
                listOfNotNull(IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java))

            Intent.ACTION_SEND_MULTIPLE ->
                IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java)
                    .orEmpty()
                    .filterNotNull()

            else -> return false
        }

        val text = intent.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString().orEmpty()
        val threadId = intent.getStringExtra(Intent.EXTRA_SHORTCUT_ID)?.takeIf { it.isNotBlank() }

        intent.removeExtra(Intent.EXTRA_STREAM)
        intent.removeExtra(Intent.EXTRA_TEXT)
        intent.removeExtra(Intent.EXTRA_SHORTCUT_ID)

        PendingShare.offer(SharePayload(uris = uris, text = text, threadId = threadId))
        return true
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return
        if (isGranted(Manifest.permission.POST_NOTIFICATIONS)) return
        notificationLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
    }

    /**
     * Mirrors ui/ui.go:225-238. The engine routes a message to whichever of the
     * user's devices is actually being used, and a device only counts as active
     * while it keeps saying so; a missed heartbeat means this phone stops
     * winning that race and the message is announced elsewhere instead.
     */
    private fun driveEngineForegroundState() {
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.RESUMED) {
                var announced = false
                try {
                    while (true) {
                        val client = EngineHolder.client
                        if (client != null) {
                            // The engine may still have been starting when the
                            // activity resumed, so foreground is announced on
                            // the first tick that finds a live client.
                            if (!announced) {
                                client.setForeground(true)
                                announced = true
                            }
                            client.currentDeviceActive()
                        }
                        delay(HEARTBEAT_MILLIS)
                    }
                } finally {
                    if (announced) {
                        withContext(NonCancellable) { EngineHolder.client?.setForeground(false) }
                    }
                }
            }
        }
    }

    private fun isGranted(permission: String): Boolean =
        ContextCompat.checkSelfPermission(this, permission) == PackageManager.PERMISSION_GRANTED

    companion object {
        /** Thread UUID to open, set by notifications and conversation shortcuts. */
        const val EXTRA_THREAD_ID = "thread_id"

        private const val HEARTBEAT_MILLIS = 5_000L
    }
}

/**
 * A runtime permission that is asked for at the moment it is needed.
 *
 * CAMERA is only ever used by the QR screens; requesting it at launch, for a
 * permission most sessions never exercise, is exactly the kind of prompt that
 * teaches users to deny by reflex.
 */
@Stable
class PermissionState internal constructor(private val onRequest: () -> Unit) {

    var granted: Boolean by mutableStateOf(false)
        internal set

    fun request() {
        if (!granted) onRequest()
    }
}

/** Installed by [MainActivity]; QR screens call `LocalCameraPermission.current.request()`. */
val LocalCameraPermission = staticCompositionLocalOf<PermissionState> {
    error("LocalCameraPermission is only available below MainActivity's content")
}
