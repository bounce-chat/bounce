package chat.bounce.ui.conversation

import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.paneTitle
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import chat.bounce.R
import chat.bounce.ui.theme.LocalBounceColors
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

/**
 * The whole of a message that [ChatBubble] had to truncate.
 *
 * A dialog rather than a route because it is a detail view of a single row with
 * no state of its own to restore, and because the conversation must stay
 * underneath it - closing this is meant to feel like putting the bubble back,
 * not like navigating back.
 *
 * @param onCopy receives the full text, never the truncated form the bubble shows.
 */
@Composable
fun ExpandedMessageDialog(
    text: String,
    senderName: String,
    timestamp: Long,
    outgoing: Boolean,
    onCopy: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    val bounce = LocalBounceColors.current
    // The same rule the inline bubble uses, so the expanded copy is the same
    // text on the same colour and reads as the bubble rather than a new screen.
    val contentColor = MaterialTheme.colorScheme.onSurface
    val title = stringResource(R.string.readmore_dialog_title)

    Dialog(
        onDismissRequest = onDismiss,
        // usePlatformDefaultWidth would cap this at the platform's alert width,
        // which is far too narrow for 15000 characters of prose.
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .fillMaxSize()
                // The card below fills most of the window, so the platform's own
                // dismiss-on-outside-touch has almost nothing left to hit. This
                // is the "outside": everything the card does not cover. Left
                // transparent because the dialog window already dims the
                // conversation behind it.
                .pointerInput(Unit) { detectTapGestures { onDismiss() } },
        ) {
            Surface(
                shape = RoundedCornerShape(20.dp),
                color = MaterialTheme.colorScheme.surface,
                tonalElevation = 3.dp,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp, vertical = 32.dp)
                    // Swallows taps that land on the card so they do not reach
                    // the dismiss handler above.
                    .pointerInput(Unit) { detectTapGestures { } }
                    .semantics { paneTitle = title },
            ) {
                Column(Modifier.padding(12.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(
                                text = senderName,
                                style = MaterialTheme.typography.titleMedium,
                                fontWeight = FontWeight.SemiBold,
                                color = MaterialTheme.colorScheme.onSurface,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            Text(
                                text = formatFullTimestamp(timestamp),
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        IconButton(onClick = { onCopy(text) }) {
                            Icon(
                                imageVector = Icons.Outlined.ContentCopy,
                                contentDescription = stringResource(R.string.conv_copy_text),
                            )
                        }
                        IconButton(onClick = onDismiss) {
                            Icon(
                                imageVector = Icons.Filled.Close,
                                contentDescription = stringResource(R.string.readmore_close),
                            )
                        }
                    }

                    Spacer(Modifier.height(8.dp))

                    Surface(
                        shape = RoundedCornerShape(18.dp),
                        color = if (outgoing) bounce.outgoingBubble else bounce.incomingBubble,
                        // fill = false so a message that only just crossed the
                        // truncation threshold gets a bubble its own size rather
                        // than a screen-tall one with a puddle of text at the top.
                        modifier = Modifier
                            .fillMaxWidth()
                            .weight(1f, fill = false),
                    ) {
                        Column(
                            Modifier
                                .verticalScroll(rememberScrollState())
                                .padding(horizontal = 14.dp, vertical = 12.dp),
                        ) {
                            Text(
                                text = rememberLinkified(text, contentColor),
                                style = MaterialTheme.typography.bodyLarge,
                                color = contentColor,
                            )
                        }
                    }
                }
            }
        }
    }
}

/**
 * Date as well as time: the bubble's bare clock time is only unambiguous in the
 * conversation, where the day separators supply the rest.
 */
private val fullTimestampFormatter: DateTimeFormatter =
    DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT)

private fun formatFullTimestamp(epochSeconds: Long): String =
    Instant.ofEpochSecond(epochSeconds)
        .atZone(ZoneId.systemDefault())
        .format(fullTimestampFormatter)
