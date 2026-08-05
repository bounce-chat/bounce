package chat.bounce.ui.conversation

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.FileDownload
import androidx.compose.material.icons.outlined.SaveAlt
import androidx.compose.material.icons.outlined.Timer
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withLink
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import chat.bounce.R
import chat.bounce.data.ConversationItem
import chat.bounce.data.ImageCache
import chat.bounce.data.VideoInfo
import chat.bounce.data.VideoThumbnails
import chat.bounce.data.deliveryStateOf
import chat.bounce.engine.EngineHolder
import chat.bounce.engine.FileAttachment
import chat.bounce.engine.ImageAttachment
import chat.bounce.goengine.Goengine
import chat.bounce.engine.User
import chat.bounce.ui.components.Avatar
import chat.bounce.ui.components.DeliveryStatusIcon
import chat.bounce.ui.components.scaledDp
import chat.bounce.ui.theme.LocalBounceColors
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale
import java.util.UUID

/**
 * Where a message sits in a run of consecutive messages from one author.
 * Corners tighten on the joined edges and the decorations - avatar, timestamp,
 * delivery state - collapse onto the last bubble of the run.
 */
data class BubbleGrouping(val firstInRun: Boolean, val lastInRun: Boolean)

/**
 * One message.
 *
 * ConversationItem.Message mirrors the DirectMessage/GroupMessage DTOs in
 * engine/Models.kt: `id, authorId, writtenAt, expiresAt, text, seen,
 * undeliverable, imageAttachments, fileAttachments, readReceipts, deliveredTo`.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun ChatBubble(
    message: ConversationItem.Message,
    outgoing: Boolean,
    isGroup: Boolean,
    author: User?,
    currentUserId: String,
    grouping: BubbleGrouping,
    fileProgress: Map<String, Double>,
    onImageClick: (ImageAttachment) -> Unit,
    onDownloadFile: (FileAttachment) -> Unit,
    onCancelDownload: (FileAttachment) -> Unit,
    /** A video ready to play, with the absolute path of its blob. */
    onPlayVideo: (FileAttachment, String) -> Unit,
    onCopyText: (String) -> Unit,
    onSaveAttachments: (List<Pair<String, String>>) -> Unit,
    /** Save a single completed attachment to a user-chosen location. */
    onSaveAttachment: (FileAttachment) -> Unit,
    modifier: Modifier = Modifier,
) {
    val bounce = LocalBounceColors.current
    var menuOpen by remember { mutableStateOf(false) }
    var expanded by remember { mutableStateOf(false) }

    // Null for the overwhelming majority of messages; non-null is the truncated
    // body, and doubles as the flag for showing the Read more affordance.
    val truncated = remember(message.text) { truncatedForBubble(message.text) }

    val showAvatarGutter = !outgoing && isGroup
    val showSenderName = !outgoing && isGroup && grouping.firstInRun
    val contentColor = MaterialTheme.colorScheme.onSurface

    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(
                start = 8.dp,
                end = 8.dp,
                top = if (grouping.firstInRun) 6.dp else 1.dp,
                bottom = if (grouping.lastInRun) 6.dp else 1.dp,
            ),
        horizontalArrangement = if (outgoing) Arrangement.End else Arrangement.Start,
        verticalAlignment = Alignment.Bottom,
    ) {
        if (showAvatarGutter) {
            // The gutter is always reserved so merged bubbles stay flush with
            // the one carrying the avatar.
            Box(Modifier.size(AVATAR_SIZE)) {
                if (grouping.lastInRun) {
                    Avatar(
                        id = message.authorId,
                        name = author?.displayName.orEmpty(),
                        images = author?.images.orEmpty(),
                        size = AVATAR_SIZE,
                    )
                }
            }
            Spacer(Modifier.width(6.dp))
        }

        Box {
            Surface(
                shape = bubbleShape(outgoing, grouping),
                color = if (outgoing) bounce.outgoingBubble else bounce.incomingBubble,
                modifier = Modifier
                    .widthIn(max = BUBBLE_MAX_WIDTH)
                    .combinedClickable(
                        onClick = {},
                        onLongClick = { menuOpen = true },
                    ),
            ) {
                Column(Modifier.padding(horizontal = 12.dp, vertical = 8.dp)) {
                    if (showSenderName) {
                        Text(
                            text = author?.displayName.orEmpty(),
                            style = MaterialTheme.typography.labelLarge,
                            fontWeight = FontWeight.SemiBold,
                            color = senderColor(message.authorId),
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.padding(bottom = 2.dp),
                        )
                    }

                    if (message.imageAttachments.isNotEmpty()) {
                        ImageAttachments(
                            attachments = message.imageAttachments,
                            fileProgress = fileProgress,
                            onClick = onImageClick,
                        )
                    }

                    message.fileAttachments.forEach { file ->
                        FileOrVideoAttachment(
                            attachment = file,
                            progress = fileProgress[file.id],
                            contentColor = contentColor,
                            onDownload = { onDownloadFile(file) },
                            onCancel = { onCancelDownload(file) },
                            onSave = onSaveAttachment,
                            onPlay = onPlayVideo,
                        )
                    }

                    if (message.text.isNotBlank()) {
                        Text(
                            text = rememberLinkified(truncated ?: message.text, contentColor),
                            style = MaterialTheme.typography.bodyLarge,
                            color = contentColor,
                            modifier = Modifier.padding(
                                top = if (message.imageAttachments.isEmpty() && message.fileAttachments.isEmpty()) 0.dp else 6.dp,
                            ),
                        )
                        if (truncated != null) {
                            val readMoreDescription =
                                stringResource(R.string.readmore_read_more_description)
                            Text(
                                text = stringResource(R.string.readmore_read_more),
                                style = MaterialTheme.typography.bodyMedium,
                                fontWeight = FontWeight.Bold,
                                color = MaterialTheme.colorScheme.primary,
                                modifier = Modifier
                                    // onLongClick is forwarded rather than left to
                                    // the Surface: a child click handler consumes
                                    // the gesture, so without it the context menu
                                    // would have a dead spot over this one line.
                                    .combinedClickable(
                                        onLongClick = { menuOpen = true },
                                        onClickLabel = stringResource(R.string.readmore_show_full_message),
                                        onClick = { expanded = true },
                                    )
                                    // Inside the click target, not outside it: the
                                    // label is a single short line and needs every
                                    // pixel of touch area it can get.
                                    .padding(top = 4.dp, bottom = 2.dp)
                                    .semantics { contentDescription = readMoreDescription },
                            )
                        }
                    }

                    if (grouping.lastInRun) {
                        MessageMeta(
                            message = message,
                            outgoing = outgoing,
                            currentUserId = currentUserId,
                            contentColor = contentColor,
                        )
                    }
                }
            }

            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                if (message.text.isNotBlank()) {
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.conv_copy_text)) },
                        leadingIcon = { Icon(Icons.Outlined.ContentCopy, contentDescription = null) },
                        onClick = {
                            menuOpen = false
                            onCopyText(message.text)
                        },
                    )
                }
                val attachmentCount = message.imageAttachments.size + message.fileAttachments.size
                if (attachmentCount > 0) {
                    DropdownMenuItem(
                        text = {
                            Text(
                                stringResource(
                                    if (attachmentCount == 1) R.string.conv_save_attachment
                                    else R.string.conv_save_attachments,
                                )
                            )
                        },
                        leadingIcon = { Icon(Icons.Outlined.SaveAlt, contentDescription = null) },
                        onClick = {
                            menuOpen = false
                            onSaveAttachments(
                                message.imageAttachments.map { it.id to it.name } +
                                    message.fileAttachments.map { it.id to it.name },
                            )
                        },
                    )
                }
            }
        }
    }

    if (expanded) {
        ExpandedMessageDialog(
            text = message.text,
            senderName = if (outgoing) {
                stringResource(R.string.conv_you)
            } else {
                author?.displayName?.takeIf { it.isNotBlank() }
                    ?: stringResource(R.string.readmore_unknown_sender)
            },
            timestamp = message.writtenAt,
            outgoing = outgoing,
            // The full text, so copying from the overlay and copying from the
            // context menu produce the same thing.
            onCopy = onCopyText,
            onDismiss = { expanded = false },
        )
    }
}

/**
 * Roughly ten lines at [BUBBLE_MAX_WIDTH] - past that one message starts to own
 * the screen and the conversation around it stops being readable.
 */
private const val TRUNCATE_AT = 500

/**
 * Truncation only earns its tap when there is a real amount left to reveal:
 * reflowing 520 characters into "500 characters plus Read more" hides nothing
 * and costs an interaction, so nothing under threshold + 20% is touched.
 */
private const val TRUNCATE_MIN_LENGTH = TRUNCATE_AT * 6 / 5

/** How far back a word boundary may be hunted before the cut is forced. */
private const val MAX_WORD_BACKTRACK = 48

private val WORD_BREAKS = charArrayOf(' ', '\n', '\t')

/**
 * The bubble-sized form of [text], or null when it is short enough to show whole.
 */
internal fun truncatedForBubble(text: String): String? {
    if (text.length <= TRUNCATE_MIN_LENGTH) return null

    val breakAt = text.lastIndexOfAny(WORD_BREAKS, startIndex = TRUNCATE_AT)
    // Falls back to the hard limit for text that has no break in that window -
    // a long URL, or a CJK message, which is written without spaces at all.
    var cut = if (breakAt >= TRUNCATE_AT - MAX_WORD_BACKTRACK) breakAt else TRUNCATE_AT
    // A cut at a fixed index can land between the halves of a surrogate pair,
    // which leaves the preview ending in a replacement glyph.
    if (Character.isHighSurrogate(text[cut - 1])) cut--

    return text.substring(0, cut).trimEnd() + "\u2026"
}

// The delivery states and their derivation moved to data/DeliveryState.kt, so
// the thread list can render exactly the same thing from the same rules.

@Composable
private fun ColumnScope.MessageMeta(
    message: ConversationItem.Message,
    outgoing: Boolean,
    currentUserId: String,
    contentColor: Color,
) {
    val muted = contentColor.copy(alpha = 0.6f)
    Row(
        modifier = Modifier
            .align(Alignment.End)
            .padding(top = 3.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Text(
            text = formatTime(message.writtenAt),
            style = MaterialTheme.typography.labelSmall,
            color = muted,
        )
        // Retention is set per thread but stamped onto each message at send
        // time, so this marks the individual bubble: an old message keeps the
        // retention it was sent under even after the thread's setting changes.
        // Shown on incoming messages too - the message disappearing is a fact
        // about it, not about who wrote it.
        if (message.expiresAt != 0L) {
            Icon(
                imageVector = Icons.Outlined.Timer,
                contentDescription = stringResource(R.string.conv_disappearing),
                tint = muted,
                modifier = Modifier.size(META_ICON_SIZE.scaledDp(META_ICON_MAX)),
            )
        }
        if (outgoing) {
            DeliveryStatusIcon(
                state = deliveryStateOf(message, currentUserId),
                mutedColor = muted,
                size = META_ICON_SIZE.scaledDp(META_ICON_MAX),
            )
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun ImageAttachments(
    attachments: List<ImageAttachment>,
    fileProgress: Map<String, Double>,
    onClick: (ImageAttachment) -> Unit,
) {
    // A lone image keeps its aspect ratio; a set is tiled square so the grid
    // stays rectangular however the photos were shot.
    if (attachments.size == 1) {
        val attachment = attachments.first()
        val ratio = if (attachment.width > 0 && attachment.height > 0) {
            attachment.width.toFloat() / attachment.height.toFloat()
        } else {
            4f / 3f
        }
        ImageAttachmentView(
            attachment = attachment,
            progressKey = fileProgress[attachment.id],
            onClick = { onClick(attachment) },
            modifier = Modifier
                .width(IMAGE_BUDGET)
                .height((IMAGE_BUDGET / ratio).coerceIn(IMAGE_MIN_HEIGHT, IMAGE_MAX_HEIGHT)),
        )
    } else {
        FlowRow(
            horizontalArrangement = Arrangement.spacedBy(IMAGE_GRID_SPACING),
            verticalArrangement = Arrangement.spacedBy(IMAGE_GRID_SPACING),
            maxItemsInEachRow = 2,
            modifier = Modifier.width(IMAGE_BUDGET),
        ) {
            // Weighted rather than a fixed tile size. Two tiles at (budget -
            // spacing) / 2 add up to the budget exactly in dp, but each value is
            // rounded to pixels on its own, and round(a) + round(b) + round(c)
            // can exceed round(a + b + c). At a density of 2.4375 the two tiles
            // came to 596px inside a 595px row, so FlowRow wrapped the second one
            // onto its own line and left half the bubble empty. Weights are
            // divided from the row's measured pixel width, so they always fit.
            attachments.forEach { attachment ->
                ImageAttachmentView(
                    attachment = attachment,
                    progressKey = fileProgress[attachment.id],
                    onClick = { onClick(attachment) },
                    modifier = Modifier
                        .weight(1f)
                        .aspectRatio(1f),
                )
            }
            // Holds the odd image at half width instead of letting it stretch
            // across a row of its own.
            if (attachments.size % 2 == 1) {
                Spacer(Modifier.weight(1f))
            }
        }
    }
}

@Composable
private fun ImageAttachmentView(
    attachment: ImageAttachment,
    progressKey: Double?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    // Re-keyed on download progress so the real bytes replace the placeholder
    // as soon as the transfer completes, without a manual refresh signal.
    val bitmap by produceState<ImageBitmap?>(null, attachment.id, progressKey) {
        value = EngineHolder.client?.let { ImageCache.load(it, attachment.id) }?.asImageBitmap()
    }
    val placeholder = remember(attachment.blurHash) {
        BlurHash.decode(attachment.blurHash, BLUR_SIZE, BLUR_SIZE)?.asImageBitmap()
    }

    Box(
        modifier = modifier
            .clip(RoundedCornerShape(12.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .clickable(onClick = onClick),
    ) {
        if (placeholder != null) {
            Image(
                bitmap = placeholder,
                contentDescription = null,
                contentScale = ContentScale.FillBounds,
                modifier = Modifier.matchParentSize(),
            )
        }
        bitmap?.let {
            Image(
                bitmap = it,
                contentDescription = stringResource(R.string.conv_photo),
                contentScale = ContentScale.Crop,
                modifier = Modifier.matchParentSize(),
            )
        }
    }
}


/**
 * Extensions worth spinning up a decoder for.
 *
 * This is only a pre-filter. Blobs are stored under a bare UUID with no
 * extension, so this reads the *sender's* filename purely to avoid running an
 * expensive MediaMetadataRetriever over every PDF in a conversation. Whether
 * the file actually plays is decided by VideoThumbnails.probe, which sniffs the
 * container - a .mp4 that is really a renamed zip falls back to a file row.
 */
private val VIDEO_EXTENSIONS = setOf(
    "mp4", "m4v", "mov", "webm", "mkv", "3gp", "3gpp", "avi", "ts", "mpeg", "mpg", "ogv",
)

private fun looksLikeVideo(name: String): Boolean =
    name.substringAfterLast('.', "").lowercase() in VIDEO_EXTENSIONS

/**
 * A file attachment, shown as a playable video when it turns out to be one.
 *
 * Availability cannot be read from [progress]: a file under the engine's 20MiB
 * embed limit is fetched automatically and never reports progress at all, so
 * null means "not downloading" rather than "not here". Probing the blob answers
 * both questions at once - it fails if the file is absent and returns null if
 * it is not a video - so that is the test, re-run whenever progress changes
 * because that is the signal a download just finished.
 */
/**
 * A file attachment in one of three states.
 *
 * The distinction that matters is whether the user has any say. The engine
 * fetches anything at or under [Goengine.EmbeddedFileLimit] on its own, so
 * offering a download button for those is a lie - there is nothing to trigger
 * and nothing to cancel. Those show a passive progress indication until the
 * bytes land, then become a save action. Only files above the limit, which are
 * seeded rather than embedded and never arrive unasked, keep a real download
 * button.
 *
 * Availability is deliberately not read from [progress]: an auto-fetched file
 * reports no progress at all, so null there means "not downloading", which
 * covers both "already here" and "still coming".
 */
@Composable
private fun FileOrVideoAttachment(
    attachment: FileAttachment,
    progress: Double?,
    contentColor: Color,
    onDownload: () -> Unit,
    onCancel: () -> Unit,
    onSave: (FileAttachment) -> Unit,
    onPlay: (FileAttachment, String) -> Unit,
) {
    val path = remember(attachment.id) { EngineHolder.client?.blobPath(attachment.id) }
    val autoFetched = attachment.size <= Goengine.EmbeddedFileLimit

    // Two independent signals because neither alone is sufficient: a file this
    // device sent is on disk without ever having been "downloaded", and a blob
    // can be recorded as downloaded while seeded from outside the blob store.
    // The engine's flag, never the presence of bytes: a blob exists from the
    // first chunk onward, so File.length() > 0 means "started", not "finished".
    // Treating a partial file as complete offers a save action that writes a
    // truncated file, and runs the video probe against a fragment that cannot
    // decode.
    //
    // No disk fallback for files this device sent - embedFile and seedFile both
    // record those with Downloaded already true (chat/file.go) - so the flag is
    // authoritative in both directions and both size classes.
    val available by produceState(false, attachment.id, progress) {
        value = runCatching { EngineHolder.client?.fileDownloaded(attachment.id) }
            .getOrNull() == true
    }

    // Keyed on progress as well as availability. `available` latches false->true
    // exactly once, so keying on it alone makes any probe failure permanent -
    // which is how a completed video stayed stuck as a plain file row after
    // being probed mid-download.
    val video by produceState<VideoInfo?>(null, attachment.id, available, progress) {
        value = if (available && path != null && looksLikeVideo(attachment.name)) {
            VideoThumbnails.probe(path)
        } else {
            null
        }
    }

    val playable = video
    when {
        playable != null && path != null -> VideoAttachmentCard(
            attachment = attachment,
            info = playable,
            contentColor = contentColor,
            onPlay = { onPlay(attachment, path) },
        )

        available -> AttachmentRow(
            attachment = attachment,
            contentColor = contentColor,
            icon = Icons.Outlined.SaveAlt,
            iconDescription = stringResource(R.string.conv_save_attachment),
            onClick = { onSave(attachment) },
        )

        // Arriving on its own: show that it is happening, but do not pretend it
        // is actionable.
        autoFetched -> AttachmentRow(
            attachment = attachment,
            contentColor = contentColor,
            subtitle = stringResource(R.string.conv_receiving),
            leading = {
                if (progress != null && progress < 1.0) {
                    CircularProgressIndicator(
                        progress = { progress.toFloat() },
                        strokeWidth = 2.dp,
                        color = contentColor.copy(alpha = DISABLED_ALPHA),
                        modifier = Modifier.size(22.dp),
                    )
                } else {
                    CircularProgressIndicator(
                        strokeWidth = 2.dp,
                        color = contentColor.copy(alpha = DISABLED_ALPHA),
                        modifier = Modifier.size(22.dp),
                    )
                }
            },
        )

        // Above the embed limit: genuinely the user's call.
        else -> FileAttachmentRow(
            attachment = attachment,
            progress = progress,
            contentColor = contentColor,
            onDownload = onDownload,
            onCancel = onCancel,
        )
    }
}

/**
 * One attachment line: a leading affordance, the filename, and a size or status
 * line. [onClick] null renders it inert rather than merely disabled-looking, so
 * a passive state cannot be tapped.
 */
@Composable
private fun AttachmentRow(
    attachment: FileAttachment,
    contentColor: Color,
    icon: androidx.compose.ui.graphics.vector.ImageVector? = null,
    iconDescription: String? = null,
    subtitle: String? = null,
    onClick: (() -> Unit)? = null,
    leading: (@Composable () -> Unit)? = null,
) {
    val faded = onClick == null
    Row(
        modifier = Modifier
            .widthIn(max = IMAGE_BUDGET)
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(40.dp), contentAlignment = Alignment.Center) {
            when {
                leading != null -> leading()
                icon != null -> Icon(
                    imageVector = icon,
                    contentDescription = iconDescription,
                    tint = contentColor,
                )
            }
        }
        Spacer(Modifier.width(8.dp))
        Column {
            Text(
                text = attachment.name,
                style = MaterialTheme.typography.bodyMedium,
                color = if (faded) contentColor.copy(alpha = DISABLED_ALPHA) else contentColor,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = subtitle ?: formatFileSize(attachment.size),
                style = MaterialTheme.typography.labelSmall,
                color = contentColor.copy(alpha = 0.6f),
            )
        }
    }
}

@Composable
private fun VideoAttachmentCard(
    attachment: FileAttachment,
    info: VideoInfo,
    contentColor: Color,
    onPlay: () -> Unit,
) {
    // Fall back to 16:9 rather than collapsing to nothing when the container
    // does not report dimensions.
    val aspect = if (info.width > 0 && info.height > 0) {
        info.width.toFloat() / info.height.toFloat()
    } else {
        16f / 9f
    }

    Column(Modifier.padding(vertical = 4.dp)) {
        Box(
            modifier = Modifier
                .width(IMAGE_BUDGET)
                .aspectRatio(aspect.coerceIn(0.5f, 2f))
                .clip(RoundedCornerShape(12.dp))
                .background(Color.Black)
                .clickable(onClick = onPlay),
            contentAlignment = Alignment.Center,
        ) {
            info.thumbnail?.let {
                Image(
                    bitmap = it.asImageBitmap(),
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.matchParentSize(),
                )
            }

            Box(
                modifier = Modifier
                    .size(52.dp)
                    .clip(CircleShape)
                    .background(Color.Black.copy(alpha = 0.55f)),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    imageVector = Icons.Filled.PlayArrow,
                    contentDescription = stringResource(R.string.conv_play_video),
                    tint = Color.White,
                    modifier = Modifier.size(34.dp),
                )
            }

            if (info.durationMs > 0) {
                Text(
                    text = VideoThumbnails.formatDuration(info.durationMs),
                    style = MaterialTheme.typography.labelSmall,
                    color = Color.White,
                    modifier = Modifier
                        .align(Alignment.BottomEnd)
                        .padding(6.dp)
                        .clip(RoundedCornerShape(4.dp))
                        .background(Color.Black.copy(alpha = 0.6f))
                        .padding(horizontal = 5.dp, vertical = 2.dp),
                )
            }
        }

        Text(
            text = attachment.name,
            style = MaterialTheme.typography.labelSmall,
            color = contentColor.copy(alpha = 0.7f),
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.width(IMAGE_BUDGET).padding(top = 4.dp),
        )
    }
}

@Composable
private fun FileAttachmentRow(
    attachment: FileAttachment,
    progress: Double?,
    contentColor: Color,
    onDownload: () -> Unit,
    onCancel: () -> Unit,
) {
    val downloading = progress != null && progress < 1.0
    Row(
        modifier = Modifier
            .widthIn(max = IMAGE_BUDGET)
            .padding(vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(40.dp), contentAlignment = Alignment.Center) {
            if (downloading) {
                CircularProgressIndicator(
                    progress = { progress?.toFloat() ?: 0f },
                    strokeWidth = 2.dp,
                    modifier = Modifier.size(38.dp),
                )
                IconButton(onClick = onCancel, modifier = Modifier.size(38.dp)) {
                    Icon(
                        imageVector = Icons.Filled.Close,
                        contentDescription = stringResource(R.string.conv_cancel_download),
                        tint = contentColor,
                        modifier = Modifier.size(18.dp),
                    )
                }
            } else {
                IconButton(onClick = onDownload, modifier = Modifier.size(40.dp)) {
                    Icon(
                        imageVector = Icons.Outlined.FileDownload,
                        contentDescription = stringResource(R.string.conv_download),
                        tint = contentColor,
                    )
                }
            }
        }
        Spacer(Modifier.width(8.dp))
        Column {
            Text(
                text = attachment.name,
                style = MaterialTheme.typography.bodyMedium,
                color = contentColor,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = formatFileSize(attachment.size),
                style = MaterialTheme.typography.labelSmall,
                color = contentColor.copy(alpha = 0.6f),
            )
        }
    }
}

private fun bubbleShape(outgoing: Boolean, grouping: BubbleGrouping): RoundedCornerShape {
    val large = 18.dp
    val joined = 4.dp
    return if (outgoing) {
        RoundedCornerShape(
            topStart = large,
            topEnd = if (grouping.firstInRun) large else joined,
            bottomEnd = if (grouping.lastInRun) large else joined,
            bottomStart = large,
        )
    } else {
        RoundedCornerShape(
            topStart = if (grouping.firstInRun) large else joined,
            topEnd = large,
            bottomEnd = large,
            bottomStart = if (grouping.lastInRun) large else joined,
        )
    }
}

/**
 * Deterministic per-user colour, ported from the desktop client's uuidToColor
 * so a sender's name is the same hue on every Bounce platform.
 */
internal fun senderColor(userId: String): Color {
    val bytes = runCatching {
        val uuid = UUID.fromString(userId)
        ByteArray(16) { index ->
            val half = if (index < 8) uuid.mostSignificantBits else uuid.leastSignificantBits
            (half shr (8 * (7 - index % 8))).toByte()
        }
    }.getOrNull() ?: return Color(0xFF382AF7)

    val hue = (((bytes[0].toInt() and 0xFF) shl 8) or (bytes[1].toInt() and 0xFF)) % 360
    val saturation = ((bytes[3].toInt() and 0xFF) % 10 + 65) / 100f
    return Color.hsv(hue.toFloat(), saturation, 0.8f)
}

/**
 * Bare-domain and scheme-prefixed URLs, with trailing sentence punctuation
 * pushed back out of the link so "see bounce.chat." does not linkify the stop.
 */
private val urlPattern = Regex(
    """(?:[a-z][a-z0-9+.-]*://)?(?:[\w-]+\.)+[a-z]{2,}(?::\d{1,5})?(?:/[^\s]*)?""",
    RegexOption.IGNORE_CASE,
)

@Composable
internal fun rememberLinkified(text: String, contentColor: Color): AnnotatedString =
    remember(text, contentColor) { linkified(text, contentColor) }

private fun linkified(text: String, contentColor: Color): AnnotatedString {
    val matches = urlPattern.findAll(text).toList()
    if (matches.isEmpty()) return AnnotatedString(text)

    val linkStyle = TextLinkStyles(
        style = SpanStyle(color = contentColor, textDecoration = TextDecoration.Underline),
    )
    return buildAnnotatedString {
        var cursor = 0
        for (match in matches) {
            append(text.substring(cursor, match.range.first))
            val display = match.value.trimEnd('.', ',', ';', ':', '!', '?', ')', ']')
            val url = if (display.contains("://")) display else "https://$display"
            withLink(LinkAnnotation.Url(url, linkStyle)) { append(display) }
            cursor = match.range.first + display.length
        }
        append(text.substring(cursor))
    }
}

private val timeFormatter: DateTimeFormatter =
    DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT)

internal fun formatTime(epochSeconds: Long): String =
    Instant.ofEpochSecond(epochSeconds).atZone(ZoneId.systemDefault()).format(timeFormatter)

internal fun formatFileSize(bytes: Long): String {
    if (bytes < 1024) return "$bytes B"
    val units = arrayOf("KB", "MB", "GB", "TB")
    var value = bytes.toDouble() / 1024
    var unit = 0
    while (value >= 1024 && unit < units.lastIndex) {
        value /= 1024
        unit++
    }
    val pattern = if (value < 10) "%.1f %s" else "%.0f %s"
    return String.format(Locale.getDefault(), pattern, value, units[unit])
}

private val AVATAR_SIZE: Dp = 28.dp
private val BUBBLE_MAX_WIDTH: Dp = 300.dp
private val IMAGE_BUDGET: Dp = 244.dp
/** Gap between grid tiles, horizontally and vertically. */
private val IMAGE_GRID_SPACING: Dp = 4.dp
private val IMAGE_MIN_HEIGHT: Dp = 96.dp
private val IMAGE_MAX_HEIGHT: Dp = 320.dp

/** BlurHash is a 4x4-ish DCT; decoding above ~24px only costs cycles. */
private const val BLUR_SIZE = 24

/** Alpha for a state the user cannot act on. */
private const val DISABLED_ALPHA = 0.45f

/**
 * Size of both meta glyphs - the expiry stopwatch and the delivery circle. They
 * are deliberately the same: side by side at different sizes one reads as more
 * important than the other, when neither is.
 *
 * In sp so they grow with the timestamp they sit beside when the system font
 * scale is raised; [META_ICON_MAX] stops that at 1.5x, past which a footnote
 * glyph starts to dominate the bubble it annotates.
 */
private val META_ICON_SIZE = 12.sp
private val META_ICON_MAX = 18.dp
