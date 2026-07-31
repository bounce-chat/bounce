package chat.bounce.ui.conversation

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.os.Build
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.foundation.Image
import android.net.Uri
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.rememberTransformableState
import androidx.compose.foundation.gestures.transformable
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.union
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.outlined.Block
import androidx.compose.material.icons.outlined.DeleteSweep
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.Notifications
import androidx.compose.material.icons.outlined.NotificationsOff
import androidx.compose.material.icons.outlined.SaveAlt
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Badge
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SmallFloatingActionButton
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import chat.bounce.R
import chat.bounce.data.ConversationItem
import chat.bounce.data.ImageCache
import chat.bounce.engine.EngineHolder
import chat.bounce.engine.FileAttachment
import chat.bounce.engine.ImageAttachment
import chat.bounce.goengine.Goengine
import chat.bounce.ui.components.Avatar
import chat.bounce.ui.theme.LocalBounceColors
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.launch
import java.time.Instant
import kotlin.math.max
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * One conversation: history, composer, and the engine's idea of "the thread the
 * user is looking at".
 *
 * The history is a reverseLayout LazyColumn, so index 0 is the newest message
 * at the bottom of the screen and the row list is built newest-first. That is
 * also why a date separator is emitted *after* the earliest item of a day - in
 * reverse it lands above it.
 */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun ConversationScreen(
    threadId: String,
    onBack: () -> Unit,
    onOpenThreadInfo: (String) -> Unit,
) {
    val context = LocalContext.current
    val factory = remember(threadId) { ConversationViewModel.factory(threadId, context) }
    val viewModel: ConversationViewModel = viewModel(key = threadId, factory = factory)
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    val listState = rememberLazyListState()
    val scope = rememberCoroutineScope()
    val snackbars = remember { SnackbarHostState() }

    val rows = remember(state.items) { buildRows(state.items) }
    val atBottom by remember { derivedStateOf { !listState.canScrollBackward } }

    // Whether the view should follow new messages down.
    //
    // Deliberately NOT just `atBottom`: inserting a row at index 0 of a
    // reverseLayout list re-anchors on the previously-first item, so the moment
    // a message lands the list has drifted up by that message's height and
    // atBottom is already false. Reading it then would turn following off every
    // time a message arrived. This only changes when the user themselves
    // finishes a scroll.
    var followBottom by remember { mutableStateOf(true) }
    LaunchedEffect(listState) {
        snapshotFlow { listState.isScrollInProgress }
            .distinctUntilChanged()
            .filter { scrolling -> !scrolling }
            .collect { followBottom = !listState.canScrollBackward }
    }

    // Re-pin once the new row actually exists. Sending is asynchronous - the
    // message only appears when the engine echoes it back via
    // DisplaySentDirectMessage - so scrolling at the moment of send scrolls to
    // where the bottom used to be.
    val newestKey = rows.firstOrNull()?.key
    LaunchedEffect(newestKey) {
        if (newestKey != null && followBottom) listState.scrollToItem(0)
    }

    // Attachments awaiting a destination. A message can carry several files, so
    // one picker names a single file and the other picks a folder for the rest.
    var pendingAttachments by remember { mutableStateOf<List<Pair<String, String>>>(emptyList()) }

    val savedLabel = stringResource(R.string.conv_save_succeeded)
    val saveFailedLabel = stringResource(R.string.conv_save_failed)

    fun reportSaved(saved: Int, total: Int) {
        scope.launch {
            snackbars.showSnackbar(if (saved == total) savedLabel else saveFailedLabel)
        }
    }

    val saveOneLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.CreateDocument(SAVE_ANY_MIME),
    ) { destination ->
        val one = pendingAttachments.firstOrNull()
        pendingAttachments = emptyList()
        if (destination != null && one != null) {
            viewModel.exportAttachment(one.first, destination) { ok -> reportSaved(if (ok) 1 else 0, 1) }
        }
    }

    val saveManyLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.OpenDocumentTree(),
    ) { tree ->
        val batch = pendingAttachments
        pendingAttachments = emptyList()
        if (tree != null && batch.isNotEmpty()) {
            viewModel.exportAttachmentsToTree(tree, batch, ::reportSaved)
        }
    }

    // The video the user tapped and the blob backing it, or null.
    var playingVideo by remember { mutableStateOf<Pair<FileAttachment, String>?>(null) }

    var viewerImages by remember { mutableStateOf<List<ImageAttachment>>(emptyList()) }
    var viewerIndex by remember { mutableStateOf(0) }

    // The engine suppresses notifications for a thread you are actively reading,
    // and this is the only signal it has for "actively".
    LaunchedEffect(atBottom) { viewModel.setScrolledDown(atBottom) }

    LaunchedEffect(listState) {
        snapshotFlow { listState.layoutInfo.visibleItemsInfo.mapNotNull { it.key as? String } }
            .distinctUntilChanged()
            .collect { keys -> keys.forEach(viewModel::markRead) }
    }

    Scaffold(
        modifier = Modifier.fillMaxSize(),
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
        snackbarHost = { SnackbarHost(snackbars) },
        topBar = {
            ConversationTopBar(
                state = state,
                onBack = onBack,
                onOpenInfo = { onOpenThreadInfo(threadId) },
                onMute = viewModel::setMutedUntil,
                onClearHistory = viewModel::clearHistory,
                onSetBlocked = viewModel::setBlocked,
            )
        },
        bottomBar = {
            Column(
                Modifier.windowInsetsPadding(WindowInsets.ime.union(WindowInsets.navigationBars)),
            ) {
                TypingIndicatorRow(state.typingNames)
                Composer(
                    text = state.draft,
                    attachments = state.pendingAttachments,
                    composerState = state.composer,
                    onTextChange = viewModel::updateDraft,
                    onAttachmentsPicked = viewModel::addAttachments,
                    onRemoveAttachment = viewModel::removeAttachment,
                    onSend = {
                        viewModel.send(state.draft, state.pendingAttachments)
                        // Sending always re-follows; the scroll itself happens
                        // when the sent message arrives.
                        followBottom = true
                    },
                )
            }
        },
    ) { padding ->
        Box(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .background(MaterialTheme.colorScheme.background),
        ) {
            if (rows.isEmpty() && !state.loading) {
                EmptyConversation()
            }

            LazyColumn(
                state = listState,
                reverseLayout = true,
                contentPadding = PaddingValues(vertical = 8.dp),
                modifier = Modifier.fillMaxSize(),
            ) {
                items(rows, key = { it.key }, contentType = { it::class }) { row ->
                    when (row) {
                        is ConversationRow.Bubble -> {
                            val copiedLabel = stringResource(R.string.conv_copied)
                            ChatBubble(
                                message = row.message,
                                outgoing = row.message.authorId == state.currentUserId,
                                isGroup = state.isGroup,
                                author = state.users[row.message.authorId],
                                currentUserId = state.currentUserId,
                                grouping = row.grouping,
                                fileProgress = state.fileProgress,
                                onImageClick = { attachment ->
                                    viewerImages = row.message.imageAttachments
                                    viewerIndex = row.message.imageAttachments.indexOf(attachment).coerceAtLeast(0)
                                },
                                onDownloadFile = { viewModel.downloadFile(it.id, it.name) },
                                onCancelDownload = { viewModel.cancelDownload(it.id) },
                                onPlayVideo = { file, path -> playingVideo = file to path },
                                onCopyText = { text ->
                                    copyToClipboard(context, text)
                                    // Android 13+ already shows its own copy confirmation.
                                    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) {
                                        scope.launch { snackbars.showSnackbar(copiedLabel) }
                                    }
                                },
                                onSaveAttachment = { file ->
                                    pendingAttachments = listOf(file.id to file.name)
                                    saveOneLauncher.launch(file.name)
                                },
                                onSaveAttachments = { items ->
                                    pendingAttachments = items
                                    // One file gets a name field; several get a
                                    // folder, rather than a picker each.
                                    if (items.size == 1) saveOneLauncher.launch(items.first().second)
                                    else if (items.isNotEmpty()) saveManyLauncher.launch(null)
                                },
                            )
                        }

                        is ConversationRow.System -> SystemRow(event = row.event)

                        is ConversationRow.DayHeader -> DateSeparator(row.timestamp)
                    }
                }
            }

            AnimatedVisibility(
                visible = !atBottom,
                enter = fadeIn() + scaleIn(),
                exit = fadeOut() + scaleOut(),
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(end = 16.dp, bottom = 16.dp),
            ) {
                JumpToBottom(unreadCount = state.unreadCount) {
                    followBottom = true
                    scope.launch { listState.jumpToBottom() }
                    viewModel.markAllRead()
                }
            }
        }
    }

    playingVideo?.let { (file, path) ->
        VideoPlayerDialog(
            path = path,
            title = file.name,
            // Shares the picker and the result toast with every other save in
            // the screen, so a video saved from the player behaves exactly like
            // one saved from its bubble.
            onSave = {
                pendingAttachments = listOf(file.id to file.name)
                saveOneLauncher.launch(file.name)
            },
            onDismiss = { playingVideo = null },
        )
    }

    if (viewerImages.isNotEmpty()) {
        ImageViewerDialog(
            images = viewerImages,
            initialIndex = viewerIndex,
            onSave = { attachment, destination ->
                viewModel.exportAttachment(attachment.id, destination) { saved ->
                    // Toast rather than the Scaffold's snackbar: the viewer is a
                    // Dialog drawn over it, so a snackbar would be hidden behind
                    // the very screen that triggered it.
                    Toast.makeText(
                        context,
                        context.getString(
                            if (saved) R.string.conv_save_succeeded else R.string.conv_save_failed,
                        ),
                        Toast.LENGTH_SHORT,
                    ).show()
                }
            },
            onDismiss = { viewerImages = emptyList() },
        )
    }
}

// --- top bar ----------------------------------------------------------------

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ConversationTopBar(
    state: ConversationUiState,
    onBack: () -> Unit,
    onOpenInfo: () -> Unit,
    onMute: (Long) -> Unit,
    onClearHistory: () -> Unit,
    onSetBlocked: (Boolean) -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    var muteDialog by remember { mutableStateOf(false) }
    var clearDialog by remember { mutableStateOf(false) }
    var blockDialog by remember { mutableStateOf(false) }
    val bounce = LocalBounceColors.current

    TopAppBar(
        colors = TopAppBarDefaults.topAppBarColors(
            containerColor = MaterialTheme.colorScheme.surface,
        ),
        navigationIcon = {
            IconButton(onClick = onBack) {
                Icon(
                    imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                    contentDescription = stringResource(R.string.conv_back),
                )
            }
        },
        title = {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                // fillMaxWidth so the dead space between the name and the
                // overflow button is part of the target rather than a gap that
                // silently does nothing. The overflow icon is a sibling in the
                // actions slot, so it keeps its own hit area.
                //
                // Not clickable on a note-to-self thread: there is no contact
                // profile to open, and the engine excludes the local profile
                // from its user list, so the destination can only render "this
                // contact is not on this device".
                modifier = Modifier
                    .fillMaxWidth()
                    .then(
                        if (state.isSelfThread) Modifier
                        else Modifier.clickable(onClick = onOpenInfo),
                    ),
            ) {
                Avatar(
                    id = state.threadId,
                    name = state.title,
                    images = state.avatarImages,
                    size = 36.dp,
                )
                Spacer(Modifier.width(10.dp))
                Column {
                    Text(
                        text = state.title,
                        style = MaterialTheme.typography.titleMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    if (!state.isSelfThread) {
                        val subtitle = when {
                            state.isGroup -> pluralStringResource(
                                R.plurals.conv_member_count,
                                state.memberCount,
                                state.memberCount,
                            )
                            state.online -> stringResource(R.string.conv_online)
                            else -> stringResource(R.string.conv_offline)
                        }
                        Text(
                            text = subtitle,
                            style = MaterialTheme.typography.labelSmall,
                            color = if (!state.isGroup && state.online) bounce.online else MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                        )
                    }
                }
            }
        },
        actions = {
            IconButton(onClick = { menuOpen = true }) {
                Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.conv_more_options))
            }
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                DropdownMenuItem(
                    text = {
                        Text(stringResource(if (state.muted) R.string.conv_unmute else R.string.conv_mute))
                    },
                    leadingIcon = {
                        Icon(
                            imageVector = if (state.muted) Icons.Outlined.Notifications else Icons.Outlined.NotificationsOff,
                            contentDescription = null,
                        )
                    },
                    onClick = {
                        menuOpen = false
                        if (state.muted) onMute(0L) else muteDialog = true
                    },
                )
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.conv_view_info)) },
                    leadingIcon = { Icon(Icons.Outlined.Info, contentDescription = null) },
                    onClick = {
                        menuOpen = false
                        onOpenInfo()
                    },
                )
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.conv_clear_history)) },
                    leadingIcon = { Icon(Icons.Outlined.DeleteSweep, contentDescription = null) },
                    onClick = {
                        menuOpen = false
                        clearDialog = true
                    },
                )
                if (!state.isSelfThread) {
                    DropdownMenuItem(
                        text = {
                            Text(stringResource(if (state.blocked) R.string.conv_unblock else R.string.conv_block))
                        },
                        leadingIcon = { Icon(Icons.Outlined.Block, contentDescription = null) },
                        onClick = {
                            menuOpen = false
                            // Unblocking is not destructive, so it skips the confirm.
                            if (state.blocked) onSetBlocked(false) else blockDialog = true
                        },
                    )
                }
            }
        },
    )

    if (muteDialog) {
        MuteDialog(
            onDismiss = { muteDialog = false },
            onPick = { until ->
                muteDialog = false
                onMute(until)
            },
        )
    }

    if (clearDialog) {
        ConfirmDialog(
            title = stringResource(R.string.conv_clear_history_title),
            message = stringResource(R.string.conv_clear_history_message),
            confirmLabel = stringResource(R.string.conv_clear),
            onConfirm = {
                clearDialog = false
                onClearHistory()
            },
            onDismiss = { clearDialog = false },
        )
    }

    if (blockDialog) {
        ConfirmDialog(
            title = if (state.isGroup) {
                stringResource(R.string.conv_block_group_title)
            } else {
                stringResource(R.string.conv_block_user_title, state.title)
            },
            message = stringResource(
                if (state.isGroup) R.string.conv_block_group_message else R.string.conv_block_user_message,
            ),
            confirmLabel = stringResource(R.string.conv_block),
            onConfirm = {
                blockDialog = false
                onSetBlocked(true)
            },
            onDismiss = { blockDialog = false },
        )
    }
}

// --- history furniture ------------------------------------------------------

@Composable
private fun DateSeparator(epochSeconds: Long) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 12.dp),
        horizontalArrangement = Arrangement.Center,
    ) {
        Surface(
            shape = CircleShape,
            color = MaterialTheme.colorScheme.surfaceVariant,
        ) {
            Text(
                text = dateLabel(epochSeconds),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 12.dp, vertical = 5.dp),
            )
        }
    }
}

@Composable
private fun dateLabel(epochSeconds: Long): String {
    val date = Instant.ofEpochSecond(epochSeconds).atZone(ZoneId.systemDefault()).toLocalDate()
    val today = LocalDate.now()
    return when {
        date == today -> stringResource(R.string.conv_today)
        date == today.minusDays(1) -> stringResource(R.string.conv_yesterday)
        date.isAfter(today.minusDays(7)) -> date.format(weekdayFormatter)
        date.year == today.year -> date.format(monthDayFormatter)
        else -> date.format(fullDateFormatter)
    }
}

@Composable
private fun TypingIndicatorRow(names: List<String>) {
    if (names.isEmpty()) return

    val label = when (names.size) {
        1 -> stringResource(R.string.conv_typing_one, names[0])
        2 -> stringResource(R.string.conv_typing_two, names[0], names[1])
        else -> stringResource(R.string.conv_typing_many)
    }
    val transition = rememberInfiniteTransition(label = "typing")

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        repeat(3) { index ->
            val alpha by transition.animateFloat(
                initialValue = 0.25f,
                targetValue = 1f,
                animationSpec = infiniteRepeatable(
                    animation = tween(600, delayMillis = index * 200, easing = LinearEasing),
                    repeatMode = RepeatMode.Reverse,
                ),
                label = "typing-dot-$index",
            )
            Box(
                Modifier
                    .size(5.dp)
                    .background(
                        color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = alpha),
                        shape = CircleShape,
                    ),
            )
            Spacer(Modifier.width(3.dp))
        }
        Spacer(Modifier.width(5.dp))
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
private fun JumpToBottom(unreadCount: Int, onClick: () -> Unit) {
    Box {
        SmallFloatingActionButton(
            onClick = onClick,
            containerColor = MaterialTheme.colorScheme.surfaceContainerHighest,
            contentColor = MaterialTheme.colorScheme.onSurface,
        ) {
            Icon(
                imageVector = Icons.Filled.KeyboardArrowDown,
                contentDescription = stringResource(R.string.conv_jump_to_bottom),
            )
        }
        if (unreadCount > 0) {
            Badge(
                containerColor = MaterialTheme.colorScheme.primary,
                modifier = Modifier.align(Alignment.TopEnd),
            ) {
                Text(
                    if (unreadCount > 999) stringResource(R.string.conv_unread_count_capped)
                    else unreadCount.toString()
                )
            }
        }
    }
}

@Composable
private fun EmptyConversation() {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(32.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = stringResource(R.string.conv_empty_title),
            style = MaterialTheme.typography.titleMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.size(6.dp))
        Text(
            text = stringResource(R.string.conv_empty_subtitle),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
    }
}

// --- dialogs ----------------------------------------------------------------

@Composable
private fun MuteDialog(onDismiss: () -> Unit, onPick: (Long) -> Unit) {
    val now = System.currentTimeMillis() / 1000
    val options = listOf(
        R.string.conv_mute_5_minutes to now + 300L,
        R.string.conv_mute_1_hour to now + 3_600L,
        R.string.conv_mute_1_day to now + 86_400L,
        R.string.conv_mute_1_week to now + 604_800L,
        R.string.conv_mute_forever to Goengine.MutedForever,
    )
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.conv_mute_title)) },
        text = {
            Column {
                options.forEach { (label, until) ->
                    Text(
                        text = stringResource(label),
                        style = MaterialTheme.typography.bodyLarge,
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable { onPick(until) }
                            .padding(vertical = 14.dp),
                    )
                }
            }
        },
        confirmButton = {},
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.conv_cancel)) }
        },
    )
}

@Composable
private fun ConfirmDialog(
    title: String,
    message: String,
    confirmLabel: String,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(message) },
        confirmButton = {
            TextButton(onClick = onConfirm) {
                Text(confirmLabel, color = MaterialTheme.colorScheme.error)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.conv_cancel)) }
        },
    )
}

/**
 * Full-screen image viewer. It lives here rather than as a navigation
 * destination because the screen's callbacks are fixed at back and thread-info,
 * and a dialog needs no route.
 */
@Composable
private fun ImageViewerDialog(
    images: List<ImageAttachment>,
    initialIndex: Int,
    onSave: (ImageAttachment, Uri) -> Unit,
    onDismiss: () -> Unit,
) {
    var index by remember(images) { mutableStateOf(initialIndex.coerceIn(0, images.lastIndex)) }
    val attachment = images[index]

    val context = LocalContext.current
    var pendingSave by remember { mutableStateOf<ImageAttachment?>(null) }

    // CreateDocument rather than writing a path ourselves: the app holds no
    // storage permission, so the picker returning a Uri is the only way to put a
    // file somewhere the user can actually find it. The suggested name is the
    // sender's filename, which is also what carries the extension.
    val saveLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.CreateDocument(SAVE_IMAGE_MIME),
    ) { destination ->
        val target = pendingSave
        pendingSave = null
        if (destination != null && target != null) {
            onSave(target, destination)
        }
    }

    // Decoded larger than the inline bubble version: zooming into a 1024px
    // decode just magnifies the resampling.
    val bitmap by produceState<ImageBitmap?>(null, attachment.id) {
        value = EngineHolder.client
            ?.let { ImageCache.load(it, attachment.id, VIEWER_MAX_DIMENSION) }
            ?.asImageBitmap()
    }

    // Reset per attachment, so paging to the next photo does not inherit the
    // previous one's zoom.
    var scale by remember(attachment.id) { mutableFloatStateOf(1f) }
    var offset by remember(attachment.id) { mutableStateOf(Offset.Zero) }
    var boxSize by remember { mutableStateOf(IntSize.Zero) }

    // Pan has to be clamped against the *displayed* image rect, not the
    // container: with ContentScale.Fit a portrait photo on a landscape screen is
    // letterboxed, and clamping to the container would let it be dragged into
    // the empty bars.
    fun clamp(raw: Offset, atScale: Float): Offset {
        if (boxSize == IntSize.Zero) return Offset.Zero
        val boxW = boxSize.width.toFloat()
        val boxH = boxSize.height.toFloat()
        val aspect = if (attachment.width > 0 && attachment.height > 0) {
            attachment.width.toFloat() / attachment.height.toFloat()
        } else {
            boxW / boxH
        }
        val fitted = if (aspect > boxW / boxH) boxW to boxW / aspect else boxH * aspect to boxH
        val maxX = max(0f, (fitted.first * atScale - boxW) / 2f)
        val maxY = max(0f, (fitted.second * atScale - boxH) / 2f)
        return Offset(raw.x.coerceIn(-maxX, maxX), raw.y.coerceIn(-maxY, maxY))
    }

    val transformState = rememberTransformableState { zoomChange, panChange, _ ->
        val next = (scale * zoomChange).coerceIn(1f, MAX_VIEWER_ZOOM)
        scale = next
        // At 1x the clamp collapses to zero, so panning an unzoomed photo does
        // nothing rather than sliding it around the screen.
        offset = clamp(offset + panChange, next)
    }

    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Box(
            Modifier
                .fillMaxSize()
                .background(MaterialTheme.colorScheme.scrim)
                .onSizeChanged { boxSize = it }
                .transformable(transformState)
                .pointerInput(attachment.id) {
                    detectTapGestures(
                        // Tapping a zoomed photo zooms out rather than closing:
                        // dismissing on the same gesture that pans is too easy
                        // to trigger by accident.
                        onTap = {
                            if (scale > 1f) {
                                scale = 1f
                                offset = Offset.Zero
                            } else {
                                onDismiss()
                            }
                        },
                        onDoubleTap = { tap ->
                            if (scale > 1f) {
                                scale = 1f
                                offset = Offset.Zero
                            } else {
                                scale = DOUBLE_TAP_ZOOM
                                // Keep the tapped point under the finger by
                                // shifting the centre toward it.
                                val dx = (boxSize.width / 2f - tap.x) * (DOUBLE_TAP_ZOOM - 1f)
                                val dy = (boxSize.height / 2f - tap.y) * (DOUBLE_TAP_ZOOM - 1f)
                                offset = clamp(Offset(dx, dy), DOUBLE_TAP_ZOOM)
                            }
                        },
                    )
                },
        ) {
            bitmap?.let {
                Image(
                    bitmap = it,
                    contentDescription = stringResource(R.string.conv_photo_count, index + 1, images.size),
                    contentScale = ContentScale.Fit,
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(16.dp)
                        .graphicsLayer {
                            scaleX = scale
                            scaleY = scale
                            translationX = offset.x
                            translationY = offset.y
                        },
                )
            }
            Row(
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(8.dp),
            ) {
                IconButton(onClick = {
                    // Remembered across the picker round trip: the callback has
                    // no way to know which image was on screen when it launched.
                    pendingSave = attachment
                    saveLauncher.launch(suggestedFileName(attachment))
                }) {
                    Icon(
                        imageVector = Icons.Outlined.SaveAlt,
                        contentDescription = stringResource(R.string.conv_save_attachment),
                        tint = MaterialTheme.colorScheme.inverseOnSurface,
                    )
                }
                IconButton(onClick = onDismiss) {
                    Icon(
                        imageVector = Icons.Filled.Close,
                        contentDescription = stringResource(R.string.conv_back),
                        tint = MaterialTheme.colorScheme.inverseOnSurface,
                    )
                }
            }
            if (images.size > 1) {
                Row(
                    modifier = Modifier
                        .align(Alignment.BottomCenter)
                        .padding(24.dp),
                    horizontalArrangement = Arrangement.spacedBy(16.dp),
                ) {
                    TextButton(onClick = { index = (index - 1 + images.size) % images.size }) {
                        Text("<", color = MaterialTheme.colorScheme.inverseOnSurface)
                    }
                    Text(
                        text = stringResource(R.string.conv_photo_count, index + 1, images.size),
                        color = MaterialTheme.colorScheme.inverseOnSurface,
                        style = MaterialTheme.typography.labelMedium,
                    )
                    TextButton(onClick = { index = (index + 1) % images.size }) {
                        Text(">", color = MaterialTheme.colorScheme.inverseOnSurface)
                    }
                }
            }
        }
    }
}

// --- row model --------------------------------------------------------------

private sealed interface ConversationRow {
    val key: String

    data class Bubble(
        val message: ConversationItem.Message,
        val grouping: BubbleGrouping,
    ) : ConversationRow {
        override val key: String get() = message.id
    }

    data class System(val event: ConversationItem.SystemEvent) : ConversationRow {
        override val key: String get() = event.id
    }

    data class DayHeader(val epochDay: Long, val timestamp: Long) : ConversationRow {
        override val key: String get() = "date-$epochDay"
    }
}

/**
 * Turns the chronological history into the newest-first row list the reversed
 * LazyColumn wants, inserting a date separator each time the day changes and
 * tagging every bubble with its position in its author's run.
 */
private fun buildRows(items: List<ConversationItem>): List<ConversationRow> {
    if (items.isEmpty()) return emptyList()

    val zone = ZoneId.systemDefault()
    val days = LongArray(items.size) { epochDay(items[it].sortTime, zone) }
    val rows = ArrayList<ConversationRow>(items.size + 8)

    for (index in items.indices.reversed()) {
        when (val item = items[index]) {
            is ConversationItem.Message ->
                rows.add(ConversationRow.Bubble(item, grouping(items, days, index)))
            is ConversationItem.SystemEvent ->
                rows.add(ConversationRow.System(item))
            else -> Unit
        }
        if (index == 0 || days[index - 1] != days[index]) {
            rows.add(ConversationRow.DayHeader(days[index], items[index].sortTime))
        }
    }
    return rows
}

private fun grouping(items: List<ConversationItem>, days: LongArray, index: Int): BubbleGrouping {
    val message = items[index] as ConversationItem.Message
    val previous = items.getOrNull(index - 1) as? ConversationItem.Message
    val next = items.getOrNull(index + 1) as? ConversationItem.Message

    val continuesFrom = previous != null &&
        previous.authorId == message.authorId &&
        message.writtenAt - previous.writtenAt <= MERGE_WINDOW_SECONDS &&
        days[index - 1] == days[index]
    val continuesInto = next != null &&
        next.authorId == message.authorId &&
        next.writtenAt - message.writtenAt <= MERGE_WINDOW_SECONDS &&
        days[index + 1] == days[index]

    return BubbleGrouping(firstInRun = !continuesFrom, lastInRun = !continuesInto)
}

/** ConversationItem has no shared timestamp accessor assumed here; this derives one. */
private val ConversationItem.sortTime: Long
    get() = when (this) {
        is ConversationItem.Message -> writtenAt
        is ConversationItem.SystemEvent -> timestamp
        else -> 0L
    }

private fun epochDay(epochSeconds: Long, zone: ZoneId): Long =
    Instant.ofEpochSecond(epochSeconds).atZone(zone).toLocalDate().toEpochDay()

private fun copyToClipboard(context: Context, text: String) {
    val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager ?: return
    clipboard.setPrimaryClip(ClipData.newPlainText("bounce-message", text))
}

private val weekdayFormatter: DateTimeFormatter = DateTimeFormatter.ofPattern("EEEE")
private val monthDayFormatter: DateTimeFormatter = DateTimeFormatter.ofPattern("MMMM d")
private val fullDateFormatter: DateTimeFormatter = DateTimeFormatter.ofPattern("MMMM d, yyyy")

/** The desktop client merges bubbles from one author inside a 5 minute window. */
private const val MERGE_WINDOW_SECONDS = 300L

/**
 * Scrolls to the newest message without the stutter of a long animated scroll.
 *
 * animateScrollToItem walks the list, and because chat rows are variable height
 * LazyColumn cannot know the distance up front - it measures as it goes and
 * repeatedly re-targets, which reads as several pauses on the way down. Jumping
 * to within a screenful first makes the distance known and bounded, so only the
 * final stretch animates.
 */
private suspend fun LazyListState.jumpToBottom() {
    if (firstVisibleItemIndex > SMOOTH_SCROLL_ITEMS) scrollToItem(SMOOTH_SCROLL_ITEMS)
    animateScrollToItem(0)
}

private const val SMOOTH_SCROLL_ITEMS = 12

/**
 * Full-screen decode ceiling. Bounce embeds attachments up to 20 MiB, and a
 * 2560px long edge is ~20 MB of ARGB - enough detail to be worth zooming into,
 * and still something the LruCache (1/8 of heap) can hold and evict.
 */
/**
 * A wildcard subtype so one launcher covers jpeg/png/webp/gif. The real type is
 * carried by the extension on the suggested filename, which every document
 * provider honours; pinning a concrete type here would mean re-registering the
 * launcher every time the user pages to an image of a different format.
 */
private const val SAVE_IMAGE_MIME = "image/*"

/** As above, but the bubble menu can also be saving a non-image file. */
private const val SAVE_ANY_MIME = "*/*"

/** The sender's filename, or something sane when they sent one without a name. */
private fun suggestedFileName(attachment: ImageAttachment): String {
    val name = attachment.name.trim()
    if (name.isNotEmpty()) return name
    return "bounce-image-${attachment.id.take(8)}.jpg"
}

private const val VIEWER_MAX_DIMENSION = 2560
private const val MAX_VIEWER_ZOOM = 6f
private const val DOUBLE_TAP_ZOOM = 2.5f
