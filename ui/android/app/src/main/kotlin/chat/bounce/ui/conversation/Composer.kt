package chat.bounce.ui.conversation

import android.graphics.BitmapFactory
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.InsertDriveFile
import androidx.compose.material.icons.outlined.PhotoLibrary
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.IconButtonDefaults
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
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import chat.bounce.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * The input row: attachments, a growing text field, and send.
 *
 * Picked media is handed upward as content:// URIs and staged to real files by
 * [ConversationViewModel.addAttachments] - the Go engine reads attachments off
 * the filesystem by path and cannot resolve a content URI.
 */
@Composable
fun Composer(
    text: String,
    attachments: List<PendingAttachment>,
    composerState: ComposerState,
    onTextChange: (String) -> Unit,
    onAttachmentsPicked: (List<Uri>) -> Unit,
    onRemoveAttachment: (String) -> Unit,
    onSend: () -> Unit,
    modifier: Modifier = Modifier,
    maxCharacters: Int = ConversationViewModel.MAX_CHARACTERS,
) {
    val context = LocalContext.current
    var attachMenuOpen by remember { mutableStateOf(false) }

    // Both launchers are registered unconditionally; which one runs is decided
    // at tap time, since rememberLauncherForActivityResult cannot be conditional.
    val photoPicker = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(MAX_PICKED_ITEMS),
    ) { uris -> onAttachmentsPicked(uris) }

    val documentPicker = rememberLauncherForActivityResult(
        ActivityResultContracts.OpenMultipleDocuments(),
    ) { uris -> onAttachmentsPicked(uris) }

    Surface(modifier = modifier.fillMaxWidth(), color = MaterialTheme.colorScheme.surface) {
        Column {
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)

            if (composerState is ComposerState.Disabled) {
                Text(
                    text = stringResource(composerState.reason),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 24.dp, vertical = 20.dp),
                )
                return@Column
            }

            if (attachments.isNotEmpty()) {
                AttachmentStrip(attachments = attachments, onRemove = onRemoveAttachment)
            }

            val remaining = maxCharacters - text.codePointCount(0, text.length)
            if (remaining <= CHARACTER_WARNING_THRESHOLD) {
                Text(
                    text = stringResource(R.string.conv_character_count, maxCharacters - remaining, maxCharacters),
                    style = MaterialTheme.typography.labelSmall,
                    color = if (remaining <= 0) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier
                        .align(Alignment.End)
                        .padding(end = 20.dp, top = 6.dp),
                )
            }

            Row(
                modifier = Modifier.padding(horizontal = 6.dp, vertical = 6.dp),
                verticalAlignment = Alignment.Bottom,
            ) {
                Box {
                    IconButton(onClick = { attachMenuOpen = true }) {
                        Icon(
                            imageVector = Icons.Filled.Add,
                            contentDescription = stringResource(R.string.conv_attach),
                            tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    DropdownMenu(expanded = attachMenuOpen, onDismissRequest = { attachMenuOpen = false }) {
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.conv_attach_photos)) },
                            leadingIcon = { Icon(Icons.Outlined.PhotoLibrary, contentDescription = null) },
                            onClick = {
                                attachMenuOpen = false
                                if (ActivityResultContracts.PickVisualMedia.isPhotoPickerAvailable(context)) {
                                    photoPicker.launch(
                                        PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo),
                                    )
                                } else {
                                    documentPicker.launch(arrayOf("image/*", "video/*"))
                                }
                            },
                        )
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.conv_attach_file)) },
                            leadingIcon = { Icon(Icons.Outlined.InsertDriveFile, contentDescription = null) },
                            onClick = {
                                attachMenuOpen = false
                                documentPicker.launch(arrayOf("*/*"))
                            },
                        )
                    }
                }

                Surface(
                    shape = RoundedCornerShape(22.dp),
                    color = MaterialTheme.colorScheme.surfaceVariant,
                    modifier = Modifier
                        .weight(1f)
                        .heightIn(min = 44.dp),
                ) {
                    BasicTextField(
                        value = text,
                        onValueChange = { onTextChange(it.limitCodePoints(maxCharacters)) },
                        textStyle = MaterialTheme.typography.bodyLarge.copy(
                            color = MaterialTheme.colorScheme.onSurface,
                        ),
                        cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                        maxLines = MAX_COMPOSER_LINES,
                        keyboardOptions = KeyboardOptions(
                            capitalization = KeyboardCapitalization.Sentences,
                            imeAction = ImeAction.Default,
                        ),
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 16.dp, vertical = 12.dp),
                        decorationBox = { inner ->
                            Box(contentAlignment = Alignment.CenterStart) {
                                if (text.isEmpty()) {
                                    Text(
                                        text = stringResource(R.string.conv_message_hint),
                                        style = MaterialTheme.typography.bodyLarge,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                                inner()
                            }
                        },
                    )
                }

                Spacer(Modifier.width(6.dp))

                val canSend = text.isNotBlank() || attachments.isNotEmpty()
                FilledIconButton(
                    onClick = onSend,
                    enabled = canSend && remaining >= 0,
                    modifier = Modifier.size(44.dp),
                    colors = IconButtonDefaults.filledIconButtonColors(
                        containerColor = MaterialTheme.colorScheme.primary,
                    ),
                ) {
                    Icon(
                        imageVector = Icons.AutoMirrored.Filled.Send,
                        contentDescription = stringResource(R.string.conv_send),
                    )
                }
            }
        }
    }
}

@Composable
private fun AttachmentStrip(
    attachments: List<PendingAttachment>,
    onRemove: (String) -> Unit,
) {
    LazyRow(
        contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        modifier = Modifier.fillMaxWidth(),
    ) {
        items(attachments, key = { it.id }) { attachment ->
            Box(Modifier.size(72.dp)) {
                Box(
                    modifier = Modifier
                        .size(64.dp)
                        .align(Alignment.BottomStart)
                        .clip(RoundedCornerShape(10.dp))
                        .background(MaterialTheme.colorScheme.surfaceVariant),
                    contentAlignment = Alignment.Center,
                ) {
                    if (attachment.isImage) {
                        val thumbnail by produceState<ImageBitmap?>(null, attachment.path) {
                            value = withContext(Dispatchers.IO) { decodeThumbnail(attachment.path) }
                        }
                        thumbnail?.let {
                            Image(
                                bitmap = it,
                                contentDescription = attachment.name,
                                contentScale = ContentScale.Crop,
                                modifier = Modifier.matchParentSize(),
                            )
                        }
                    } else {
                        Column(
                            horizontalAlignment = Alignment.CenterHorizontally,
                            modifier = Modifier.padding(4.dp),
                        ) {
                            Icon(
                                imageVector = Icons.Outlined.InsertDriveFile,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            Text(
                                text = attachment.name,
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                }

                Box(
                    modifier = Modifier
                        .size(22.dp)
                        .align(Alignment.TopEnd)
                        .clip(CircleShape)
                        .background(MaterialTheme.colorScheme.surfaceContainerHighest),
                ) {
                    IconButton(onClick = { onRemove(attachment.id) }, modifier = Modifier.size(22.dp)) {
                        Icon(
                            imageVector = Icons.Filled.Close,
                            contentDescription = stringResource(R.string.conv_remove_attachment, attachment.name),
                            tint = MaterialTheme.colorScheme.onSurface,
                            modifier = Modifier.size(14.dp),
                        )
                    }
                }
            }
        }
    }
}

/** Downsamples on decode; a full-resolution photo would be wasted on a 64dp chip. */
private fun decodeThumbnail(path: String): ImageBitmap? = runCatching {
    val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    BitmapFactory.decodeFile(path, bounds)
    var sample = 1
    while (bounds.outWidth / sample > THUMBNAIL_PIXELS || bounds.outHeight / sample > THUMBNAIL_PIXELS) {
        sample *= 2
    }
    val options = BitmapFactory.Options().apply { inSampleSize = sample }
    BitmapFactory.decodeFile(path, options)?.asImageBitmap()
}.getOrNull()

private const val MAX_COMPOSER_LINES = 5
private const val MAX_PICKED_ITEMS = 10
private const val THUMBNAIL_PIXELS = 256
private const val CHARACTER_WARNING_THRESHOLD = 200
