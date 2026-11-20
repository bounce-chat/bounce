package chat.bounce.ui.components

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import chat.bounce.data.Avatars
import chat.bounce.engine.EngineHolder
import chat.bounce.ui.theme.BounceTheme
import chat.bounce.ui.theme.uuidColor

/**
 * A conversation or contact avatar.
 *
 * @param fileIds the image chain from [chat.bounce.engine.User.images] /
 *   [chat.bounce.engine.Group.images]; the newest file already on disk wins.
 * @param fallbackId the thread or user UUID, which seeds the generated avatar.
 * @param online draws a presence dot when non-null. Pass null for groups, which
 *   have no single presence.
 */
@Composable
fun Avatar(
    fileIds: List<String>,
    fallbackId: String,
    fallbackName: String,
    size: Dp,
    online: Boolean? = null,
    modifier: Modifier = Modifier,
) {
    val sizePx = with(LocalDensity.current) { size.roundToPx() }

    // Keyed on the identity rather than only the ids: recycling a row in a
    // LazyColumn would otherwise show the previous person's face until the new
    // load lands.
    var bitmap by remember(fallbackId) { mutableStateOf<ImageBitmap?>(null) }

    // Profile images arrive asynchronously (FileCompleted), so fileIds going
    // from empty to populated has to re-run the load.
    LaunchedEffect(fileIds, fallbackId, fallbackName, sizePx) {
        bitmap = loadAvatar(fileIds, fallbackId, fallbackName, sizePx)?.asImageBitmap()
    }

    Box(modifier = modifier.size(size)) {
        val loaded = bitmap
        if (loaded != null) {
            Image(
                bitmap = loaded,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier
                    .fillMaxSize()
                    .clip(CircleShape),
            )
        } else {
            // Only visible for the frame or two before the bitmap lands, and for
            // as long as the engine has not started yet.
            InitialsAvatar(fallbackId, fallbackName, size)
        }

        if (online != null) {
            PresenceDot(
                online = online,
                size = (size * 0.3f).coerceIn(10.dp, 18.dp),
                modifier = Modifier.align(Alignment.BottomEnd),
            )
        }
    }
}

/**
 * Convenience form for the common `(id, name, images)` shape, where the entity's
 * own UUID is also the fallback seed.
 */
@Composable
fun Avatar(
    id: String,
    name: String,
    images: List<String>,
    size: Dp,
    modifier: Modifier = Modifier,
) {
    Avatar(
        fileIds = images,
        fallbackId = id,
        fallbackName = name,
        size = size,
        online = null,
        modifier = modifier,
    )
}

@Composable
private fun InitialsAvatar(id: String, name: String, size: Dp) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .fillMaxSize()
            .clip(CircleShape)
            .background(uuidColor(id)),
    ) {
        Text(
            text = Avatars.initialsFor(name),
            color = Color.White,
            fontWeight = FontWeight.Medium,
            // A fixed ratio of the box, so initials fill the circle the same way
            // at 32dp in a header and at 128dp on a profile screen.
            fontSize = (size.value * 0.38f).sp,
            // The name is already rendered beside every avatar that uses this.
            modifier = Modifier.clearAndSetSemantics {},
        )
    }
}

@Composable
private fun PresenceDot(online: Boolean, size: Dp, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .size(size)
            // The ring is the page background, so the dot reads as cut out of the
            // avatar rather than floating on top of it.
            .background(MaterialTheme.colorScheme.surface, CircleShape)
            .padding(2.dp)
            .background(
                if (online) BounceTheme.colors.online else BounceTheme.colors.offline,
                CircleShape,
            ),
    )
}

/**
 * Single call site for the data layer's renderer.
 *
 * Null only while the engine is still starting: [Avatars.avatarBitmap] needs a live
 * [chat.bounce.engine.EngineClient] to read blobs, and returns a generated
 * avatar rather than null once it has one.
 */
private suspend fun loadAvatar(
    fileIds: List<String>,
    fallbackId: String,
    fallbackName: String,
    sizePx: Int,
): Bitmap? {
    val client = EngineHolder.client ?: return null
    return runCatching {
        Avatars.avatarBitmap(client, fileIds, fallbackId, fallbackName, sizePx)
    }.getOrNull()
}
