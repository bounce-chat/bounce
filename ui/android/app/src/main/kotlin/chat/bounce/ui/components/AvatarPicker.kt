package chat.bounce.ui.components

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.ImageDecoder
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PhotoCamera
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.ByteArrayOutputStream

/**
 * Avatar selection shared by profile creation, profile editing and new-group.
 *
 * Everything here funnels a `content://` Uri from the photo picker into the
 * PNG byte array the engine wants. The engine stores avatars as ordinary
 * attachments that replicate to every sync device and every peer, so keeping
 * them small is a bandwidth decision, not a memory one.
 */

// Avatars replicate to all of a user's devices and to every peer that can see
// the profile, over Tor. A megabyte is already generous for a circle rendered
// at 128dp.
const val AVATAR_MAX_BYTES: Int = 1_000_000

private const val AVATAR_START_DIMENSION = 512
private const val AVATAR_MIN_DIMENSION = 96

/**
 * Decodes [uri], centre-crops it to a square and returns PNG bytes no larger
 * than [maxBytes], or null if the image could not be read.
 */
suspend fun decodeAvatar(
    context: Context,
    uri: Uri,
    maxBytes: Int = AVATAR_MAX_BYTES,
): ByteArray? = withContext(Dispatchers.IO) {
    val decoded = runCatching {
        val source = ImageDecoder.createSource(context.contentResolver, uri)
        ImageDecoder.decodeBitmap(source) { decoder, info, _ ->
            // HARDWARE bitmaps live in graphics memory and cannot be read back,
            // which breaks both the crop and the PNG encode below.
            decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
            decoder.isMutableRequired = false
            // A 50MP camera shot would otherwise be fully decoded before being
            // thrown away; sample it down on the way in.
            val longest = maxOf(info.size.width, info.size.height)
            var sample = 1
            while (longest / sample > AVATAR_START_DIMENSION * 2) sample *= 2
            decoder.setTargetSampleSize(sample)
        }
    }.getOrNull() ?: return@withContext null

    var dimension = AVATAR_START_DIMENSION
    var encoded = ByteArray(0)
    while (true) {
        val square = decoded.centreSquare(dimension)
        val out = ByteArrayOutputStream()
        // PNG ignores the quality argument, so the only lever on size is the
        // pixel dimension - hence the loop rather than a quality ramp.
        square.compress(Bitmap.CompressFormat.PNG, 100, out)
        if (square !== decoded) square.recycle()

        encoded = out.toByteArray()
        if (encoded.size <= maxBytes || dimension <= AVATAR_MIN_DIMENSION) break
        dimension /= 2
    }
    decoded.recycle()
    encoded
}

private fun Bitmap.centreSquare(dimension: Int): Bitmap {
    val side = minOf(width, height)
    val cropped = if (side == width && side == height) {
        this
    } else {
        Bitmap.createBitmap(this, (width - side) / 2, (height - side) / 2, side, side)
    }
    if (side == dimension) return cropped
    val scaled = Bitmap.createScaledBitmap(cropped, dimension, dimension, true)
    if (cropped !== this && cropped !== scaled) cropped.recycle()
    return scaled
}

/**
 * Returns a callback that opens the system photo picker and hands back PNG
 * bytes. [onPicked] receives null if the user cancelled or the image was
 * unreadable.
 */
@Composable
fun rememberAvatarPicker(onPicked: (ByteArray?) -> Unit): () -> Unit {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val callback by rememberUpdatedState(onPicked)

    val launcher = rememberLauncherForActivityResult(
        ActivityResultContracts.PickVisualMedia()
    ) { uri ->
        if (uri == null) {
            callback(null)
        } else {
            scope.launch { callback(decodeAvatar(context, uri)) }
        }
    }

    return remember(launcher) {
        {
            launcher.launch(
                PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)
            )
        }
    }
}

/**
 * A circular, tappable avatar slot with a camera badge.
 *
 * [placeholder] is what shows before anything has been picked - normally the
 * shared [Avatar] composable, so an existing profile photo or the generated
 * initials circle stays visible while editing.
 */
@Composable
fun AvatarPickerButton(
    picked: ByteArray?,
    size: Dp,
    contentDescription: String?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    placeholder: @Composable () -> Unit,
) {
    val preview = remember(picked) {
        picked?.let { BitmapFactory.decodeByteArray(it, 0, it.size) }
    }

    // The circular clip belongs to the picture, not to this Box. Clipping here
    // took the camera badge with it, cutting off the corner that falls outside
    // the circle - the badge has to sit in front of the avatar, not inside it.
    Box(
        modifier = modifier
            .size(size)
            .clickable(onClick = onClick),
        contentAlignment = Alignment.BottomEnd,
    ) {
        Box(Modifier.fillMaxSize().clip(CircleShape)) {
            if (preview != null) {
                Image(
                    bitmap = preview.asImageBitmap(),
                    contentDescription = contentDescription,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize(),
                )
            } else {
                placeholder()
            }
        }

        // Drawn after the picture, so it overlaps the circle's edge.
        Box(
            modifier = Modifier
                .padding(2.dp)
                .size(size / 3.5f)
                .background(MaterialTheme.colorScheme.primary, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = Icons.Filled.PhotoCamera,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onPrimary,
                modifier = Modifier.size(size / 6f),
            )
        }
    }
}
