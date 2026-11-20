package chat.bounce.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Done
import androidx.compose.material.icons.outlined.DoneAll
import androidx.compose.material.icons.outlined.MoreHoriz
import androidx.compose.material.icons.outlined.Warning
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import chat.bounce.R
import chat.bounce.data.DeliveryState

/**
 * The delivery indicator for an outgoing message, shared by the chat bubble and
 * the thread list so the two can never drift apart.
 *
 * Every state is a circle of identical size, and only the contents change. That
 * is what keeps the four states optically the same weight: an earlier version
 * drew the read state as a filled disc and the rest as bare glyphs, so the read
 * state's visible height was larger than everything else's and a message
 * becoming read appeared to grow.
 *
 *  - [DeliveryState.OnDevice] - three dots, still only here
 *  - [DeliveryState.SyncedToMyDevices] - one check, our own devices have it
 *  - [DeliveryState.DeliveredToOthers] - two checks, somebody else's device has it
 *  - [DeliveryState.ReadByOthers] - two checks on a filled circle, somebody read it
 *  - [DeliveryState.Undeliverable] - exclamation in a triangle, the one state
 *    drawn without a circle, because it is a failure rather than a rung on the
 *    same ladder and should not read as one
 *
 * @param mutedColor colour for everything except the read fill and the failure,
 *   which should recede into whatever surface it is drawn on.
 * @param size diameter of the circle. Required rather than defaulted: the two
 *   screens want genuinely different sizes, so there is no one sensible value.
 */
@Composable
fun DeliveryStatusIcon(
    state: DeliveryState,
    mutedColor: Color,
    modifier: Modifier = Modifier,
    size: Dp,
) {
    val description = stringResource(
        when (state) {
            DeliveryState.OnDevice -> R.string.conv_status_on_device
            DeliveryState.SyncedToMyDevices -> R.string.conv_status_synced
            DeliveryState.DeliveredToOthers -> R.string.conv_status_delivered
            DeliveryState.ReadByOthers -> R.string.conv_status_read
            DeliveryState.Undeliverable -> R.string.conv_status_undeliverable
        }
    )

    if (state == DeliveryState.Undeliverable) {
        Icon(
            imageVector = Icons.Outlined.Warning,
            contentDescription = description,
            tint = MaterialTheme.colorScheme.error,
            modifier = modifier.size(size),
        )
        return
    }

    val read = state == DeliveryState.ReadByOthers

    Box(
        modifier = modifier
            .size(size)
            .then(
                if (read) {
                    // Softened rather than solid so it reads as a highlight
                    // instead of a block against the bubble.
                    Modifier.background(
                        MaterialTheme.colorScheme.primary.copy(alpha = READ_FILL_ALPHA),
                        CircleShape,
                    )
                } else {
                    Modifier.border(RING_WIDTH, mutedColor.copy(alpha = RING_ALPHA), CircleShape)
                }
            ),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = when (state) {
                DeliveryState.OnDevice -> Icons.Outlined.MoreHoriz
                DeliveryState.SyncedToMyDevices -> Icons.Outlined.Done
                else -> Icons.Outlined.DoneAll
            },
            contentDescription = description,
            // On the filled circle the checks are knocked through in the
            // circle's own content colour; on the ring they match it.
            tint = if (read) MaterialTheme.colorScheme.onPrimary else mutedColor,
            modifier = Modifier.size(size * GLYPH_RATIO),
        )
    }
}

/**
 * Glyph size as a fraction of the circle. Low enough that two checks clear the
 * ring without crowding it; DoneAll is the widest of the glyphs and sets the
 * limit.
 */
private const val GLYPH_RATIO = 0.62f

private val RING_WIDTH = 1.dp

/** The ring is scaffolding for the glyph, not a mark of its own. */
private const val RING_ALPHA = 0.45f

/** Softens the read fill so it highlights rather than blocks. */
private const val READ_FILL_ALPHA = 0.6f
