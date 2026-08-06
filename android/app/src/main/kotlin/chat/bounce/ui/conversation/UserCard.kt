package chat.bounce.ui.conversation

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import chat.bounce.R
import chat.bounce.data.Avatars
import chat.bounce.engine.EngineHolder
import chat.bounce.engine.User
import chat.bounce.ui.components.Avatar

/**
 * The card behind a group member's avatar.
 *
 * Deliberately small: who this is, and the two things worth doing about them.
 * Anything that manages the person - blocking, aliases, shared groups - lives on
 * the details screen this links to, so tapping a face in a conversation stays a
 * glance rather than a detour.
 *
 * No close button. A [Dialog] dismisses on an outside tap and on Back already,
 * and a card this small has nowhere to put one that does not compete with the
 * two actions.
 *
 * @param onViewImage called with the avatar's file ID when the user taps a real
 *   photo. Never called when the avatar is the generated one - there is nothing
 *   to open, and a viewer showing a coloured circle with initials in it would be
 *   a dead end.
 */
@Composable
fun UserCard(
    user: User,
    onViewImage: (String) -> Unit,
    onMessage: () -> Unit,
    onViewDetails: () -> Unit,
    onDismiss: () -> Unit,
) {
    val sizePx = with(LocalDensity.current) { CARD_AVATAR_SIZE.roundToPx() }

    // Null until resolved, and null forever for a generated avatar. Keyed on the
    // image chain so a picture that lands while the card is open becomes
    // tappable rather than staying inert.
    val photoId by produceState<String?>(null, user.images, sizePx) {
        value = EngineHolder.client?.let { Avatars.avatarFileId(it, user.images, sizePx) }
    }

    Dialog(onDismissRequest = onDismiss) {
        Surface(
            shape = MaterialTheme.shapes.extraLarge,
            tonalElevation = 6.dp,
            modifier = Modifier.widthIn(max = CARD_MAX_WIDTH),
        ) {
            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                modifier = Modifier.padding(24.dp),
            ) {
                val photo = photoId
                Avatar(
                    fileIds = user.images,
                    fallbackId = user.id,
                    fallbackName = user.displayName,
                    size = CARD_AVATAR_SIZE,
                    modifier = if (photo != null) {
                        // Clipped to the circle first so the ripple follows the
                        // avatar rather than boxing it.
                        Modifier
                            .clip(CircleShape)
                            .clickable { onViewImage(photo) }
                    } else {
                        Modifier
                    },
                )

                Spacer(Modifier.height(16.dp))
                Text(
                    text = user.displayName,
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.SemiBold,
                    textAlign = TextAlign.Center,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )

                Spacer(Modifier.height(20.dp))
                Button(onClick = onMessage, modifier = Modifier.fillMaxWidth()) {
                    Text(stringResource(R.string.user_card_message))
                }
                Spacer(Modifier.height(8.dp))
                OutlinedButton(onClick = onViewDetails, modifier = Modifier.fillMaxWidth()) {
                    Text(stringResource(R.string.user_card_details))
                }
            }
        }
    }
}

private val CARD_AVATAR_SIZE: Dp = 96.dp
private val CARD_MAX_WIDTH: Dp = 320.dp
