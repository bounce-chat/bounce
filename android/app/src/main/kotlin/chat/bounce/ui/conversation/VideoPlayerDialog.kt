package chat.bounce.ui.conversation

import android.view.Gravity
import android.widget.FrameLayout
import android.widget.MediaController
import android.widget.VideoView
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material3.Icon
import androidx.compose.material.icons.outlined.SaveAlt
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.paneTitle
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import chat.bounce.R

/**
 * Full-screen playback of a video attachment that is already on disk.
 *
 * Built on the platform's [VideoView] and [MediaController] rather than a
 * dedicated player library: the file is a completed download sitting in this
 * process's own private storage, so there is no streaming, adaptation or
 * authentication left to do, and the system decoder covers the whole job. The
 * engine names blobs by bare UUID with no extension, which is fine here because
 * MediaPlayer sniffs the container rather than trusting the file name.
 *
 * @param path absolute path to a blob in app-private storage.
 * @param title the attachment's filename, shown in the header.
 */
@Composable
fun VideoPlayerDialog(
    path: String,
    title: String,
    /**
     * Invoked when the viewer wants a copy. The caller owns the destination
     * picker, so this shares the save flow with the bubble and the image viewer
     * rather than opening a second one. Null hides the affordance.
     */
    onSave: (() -> Unit)? = null,
    onDismiss: () -> Unit,
) {
    // The activity context, not the application context: [MediaController] puts
    // its transport bar in its own window and needs a window token to attach it
    // to. Inside a Compose [Dialog] this resolves to the hosting activity, and
    // the token it borrows from the anchor is the dialog's own.
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current

    val displayTitle = title.ifBlank { stringResource(R.string.video_untitled) }
    val paneLabel = stringResource(R.string.video_pane_title, displayTitle)

    // Milliseconds (VideoView speaks Int positions), saved so a configuration
    // change resumes where the picture was instead of starting the file again.
    // The manifest lets MainActivity swallow orientation changes itself, so
    // rotation is already seamless - this is for the ones it does not list
    // (locale, density, font scale) and for restore after process death, where
    // the composition really is rebuilt from nothing.
    var resumePositionMs by rememberSaveable { mutableStateOf(0) }
    var failed by rememberSaveable { mutableStateOf(false) }

    val player = remember(context) { VideoView(context) }
    val controller = remember(context) { MediaController(context) }

    // VideoView shrinks its measured size to the video's aspect ratio even under
    // exact constraints, and a Compose-hosted view is placed at the host's top
    // left, so a 16:9 file in a portrait window would sit against the status bar
    // with all the empty space below it. A FrameLayout with Gravity.CENTER
    // letterboxes it symmetrically, and gives the controller a full-bleed parent
    // to anchor its transport bar to.
    val frame = remember(player) {
        FrameLayout(context).apply {
            addView(
                player,
                FrameLayout.LayoutParams(
                    FrameLayout.LayoutParams.MATCH_PARENT,
                    FrameLayout.LayoutParams.MATCH_PARENT,
                    Gravity.CENTER,
                ),
            )
        }
    }

    DisposableEffect(player, path) {
        failed = false

        player.setOnPreparedListener {
            // Anchoring here rather than at construction: VideoView re-anchors
            // the controller to the view's parent from inside openVideo(), which
            // it defers until the surface exists, so an anchor set any earlier is
            // silently replaced. onPrepared is the first point that is always
            // after it.
            controller.setAnchorView(player)
            if (resumePositionMs > 0) player.seekTo(resumePositionMs)
            player.start()
            player.keepScreenOn = true
        }

        player.setOnCompletionListener {
            player.keepScreenOn = false
            // Clearing the resume point so that a player rebuilt after the file
            // has finished replays it, rather than seeking straight back to the
            // last frame and looking stuck.
            resumePositionMs = 0
        }

        player.setOnErrorListener { _, _, _ ->
            failed = true
            player.keepScreenOn = false
            // Sniffing the container only proves what the file claims to be; a
            // device with no decoder for that profile fails right here. Returning
            // true marks the error handled, which is what stops VideoView from
            // raising its own platform AlertDialog on top of this one.
            true
        }

        player.setMediaController(controller)
        player.setVideoPath(path)

        onDispose {
            // Detaching the controller hides its floating window. That window
            // lives in the WindowManager rather than in our view tree, so nothing
            // else takes it down when the dialog goes away.
            player.setMediaController(null)
            // Releases the decoder and the surface it is bound to. A device has
            // only a handful of hardware codec instances, and a dialog dismissed
            // mid-playback would hold one of them for the life of the process.
            player.stopPlayback()
            player.keepScreenOn = false
        }
    }

    DisposableEffect(lifecycleOwner, player) {
        // Distinguishes "we paused this because the screen went away" from "the
        // viewer pressed pause on the transport bar", so returning to the app
        // does not override a deliberate pause.
        var pausedForBackground = false

        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_PAUSE -> {
                    if (player.isPlaying) {
                        // Captured here and not in onDispose: saved state is
                        // written on the way out of the activity, before the
                        // composition is torn down, so a position recorded at
                        // dispose time is always too late to be restored.
                        resumePositionMs = player.currentPosition
                        player.pause()
                        pausedForBackground = true
                    }
                    player.keepScreenOn = false
                }

                Lifecycle.Event.ON_RESUME -> {
                    if (pausedForBackground) {
                        pausedForBackground = false
                        player.start()
                        player.keepScreenOn = true
                    }
                }

                else -> Unit
            }
        }

        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    Dialog(
        onDismissRequest = onDismiss,
        // The video has to be measured against the whole window, not the
        // platform's alert width, which would leave a postage stamp in the middle
        // of the screen. dismissOnBackPress stays on so the back gesture closes
        // this the way it closes any other overlay.
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(VideoScrim)
                .semantics { paneTitle = paneLabel },
        ) {
            // Kept mounted even when playback has failed, with the message drawn
            // over it: re-adding a remembered View to a freshly created host
            // throws, and the surface behind the message is black in the error
            // state anyway.
            AndroidView(factory = { frame }, modifier = Modifier.fillMaxSize())

            if (failed) {
                Column(
                    modifier = Modifier
                        .align(Alignment.Center)
                        .widthIn(max = 320.dp)
                        .padding(24.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    Icon(
                        imageVector = Icons.Outlined.ErrorOutline,
                        contentDescription = null,
                        tint = OnVideoScrimMuted,
                    )
                    Spacer(Modifier.height(12.dp))
                    Text(
                        text = stringResource(R.string.video_playback_failed),
                        style = MaterialTheme.typography.titleMedium,
                        color = OnVideoScrim,
                        textAlign = TextAlign.Center,
                    )
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = stringResource(R.string.video_playback_failed_detail),
                        style = MaterialTheme.typography.bodyMedium,
                        color = OnVideoScrimMuted,
                        textAlign = TextAlign.Center,
                    )
                }
            }

            Row(
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .fillMaxWidth()
                    // A bright first frame would otherwise swallow white text, and
                    // the header sits directly on the picture.
                    .background(HeaderScrim)
                    .padding(start = 16.dp, end = 4.dp, top = 8.dp, bottom = 20.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = displayTitle,
                    style = MaterialTheme.typography.titleMedium,
                    color = OnVideoScrim,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (onSave != null) {
                    IconButton(onClick = onSave) {
                        Icon(
                            imageVector = Icons.Outlined.SaveAlt,
                            contentDescription = stringResource(R.string.video_save),
                            tint = OnVideoScrim,
                        )
                    }
                }
                IconButton(onClick = onDismiss) {
                    Icon(
                        imageVector = Icons.Filled.Close,
                        contentDescription = stringResource(R.string.video_close),
                        tint = OnVideoScrim,
                    )
                }
            }
        }
    }
}

/**
 * Fixed light-on-dark rather than the Material roles: this surface is black in
 * both themes, so onSurface would resolve to near-black text on it whenever the
 * app is in light mode. Video is also judged against what surrounds it, hence a
 * near-opaque backdrop instead of a translucent scrim that would let the
 * conversation bleed through the letterbox bars.
 */
private val VideoScrim = Color.Black.copy(alpha = 0.94f)
private val OnVideoScrim = Color.White
private val OnVideoScrimMuted = Color.White.copy(alpha = 0.7f)

private val HeaderScrim = Brush.verticalGradient(
    listOf(Color.Black.copy(alpha = 0.55f), Color.Transparent),
)
