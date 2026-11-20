@file:OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)

package chat.bounce.ui.threads

import androidx.activity.compose.BackHandler
import androidx.compose.animation.animateColorAsState
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.Forum
import androidx.compose.material.icons.outlined.GppBad
import androidx.compose.material.icons.outlined.Group
import androidx.compose.material.icons.outlined.NotificationsOff
import androidx.compose.material.icons.outlined.PersonAdd
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.ui.conversation.systemEventText
import chat.bounce.ui.components.Avatar
import chat.bounce.ui.components.DeliveryStatusIcon
import chat.bounce.ui.components.EmptyState
import chat.bounce.ui.components.scaledDp
import chat.bounce.ui.theme.BounceTheme

/**
 * The inbox.
 *
 * @param onNewChat opens the new-chat sheet, which is a navigation destination
 *   rather than local state so the hardware back button dismisses it.
 */
@Composable
fun ThreadListScreen(
    onOpenThread: (String) -> Unit,
    onNewChat: () -> Unit,
    onNewGroup: () -> Unit,
    onAddContact: () -> Unit,
    onOpenContacts: () -> Unit,
    onOpenDevices: () -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: ThreadListViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    var searching by rememberSaveable { mutableStateOf(false) }
    var actionsFor by remember { mutableStateOf<ThreadRow?>(null) }

    val listState = rememberLazyListState()
    // derivedStateOf so this recomposes on the transition only. Reading the
    // scroll offset directly would recompose the whole screen on every frame of
    // every fling, to answer a question whose value changes twice.
    val scrolled by remember {
        derivedStateOf {
            listState.firstVisibleItemIndex > 0 || listState.firstVisibleItemScrollOffset > 0
        }
    }

    BackHandler(enabled = searching) {
        searching = false
        viewModel.setQuery("")
    }

    // Sending a message moves that thread to the top, and the list restores
    // whatever scroll position it had before the conversation was opened - so
    // without this the thread just messaged is off screen. Not animated: this
    // runs as the screen is coming back, and an animation would read as the list
    // moving on its own after the transition finished.
    val scrollToTop by ChatRepository.scrollThreadListToTop.collectAsStateWithLifecycle()
    LaunchedEffect(scrollToTop) {
        if (scrollToTop) {
            listState.scrollToItem(0)
            ChatRepository.threadListScrolledToTop()
        }
    }

    Scaffold(
        modifier = modifier,
        topBar = {
            ThreadListTopBar(
                query = state.query,
                searching = searching,
                scrolled = scrolled,
                onQueryChange = viewModel::setQuery,
                onToggleSearch = {
                    searching = !searching
                    if (!searching) viewModel.setQuery("")
                },
                onNewGroup = onNewGroup,
                onAddContact = onAddContact,
                onOpenContacts = onOpenContacts,
                onOpenDevices = onOpenDevices,
                onOpenSettings = onOpenSettings,
                revoked = state.revoked,
            )
        },
        floatingActionButton = {
            FloatingActionButton(onClick = onNewChat) {
                Icon(Icons.Filled.Add, contentDescription = stringResource(R.string.new_chat))
            }
        },
    ) { padding ->
        Column(Modifier.padding(padding)) {
            // Revocation supersedes the offline banner rather than stacking with
            // it. Both are true at once - a revoked device is offline by
            // definition - but "reconnecting shortly" is the wrong thing to
            // imply when it never will.
            if (state.revoked) {
                Banner(
                    icon = Icons.Outlined.GppBad,
                    text = stringResource(R.string.threads_device_revoked),
                )
            } else if (!state.online) {
                Banner(icon = Icons.Outlined.CloudOff, text = stringResource(R.string.threads_offline))
            }

            if (state.syncing) SyncProgress(state.syncProgress)

            when {
                state.rows.isNotEmpty() -> LazyColumn(Modifier.fillMaxSize(), state = listState) {
                    items(state.rows, key = { it.id }) { row ->
                        ThreadRowItem(
                            row = row,
                            onClick = { onOpenThread(row.id) },
                            onLongClick = { actionsFor = row },
                            onAccept = { viewModel.acceptInvite(row.id) },
                            onDecline = { viewModel.declineInvite(row.id) },
                        )
                    }
                }

                // A non-empty inbox with no visible rows can only mean the
                // search matched nothing.
                state.hasAnyThreads -> EmptyState(
                    icon = Icons.Filled.Search,
                    title = stringResource(R.string.threads_no_results),
                )

                else -> EmptyState(
                    icon = Icons.Outlined.Forum,
                    title = stringResource(R.string.threads_empty_title),
                    description = stringResource(R.string.threads_empty_body),
                    action = {
                        Button(onClick = onNewChat) { Text(stringResource(R.string.new_chat)) }
                    },
                )
            }
        }
    }

    actionsFor?.let { row ->
        ThreadActionsSheet(row = row, viewModel = viewModel, onDismiss = { actionsFor = null })
    }
}

@Composable
private fun ThreadListTopBar(
    query: String,
    searching: Boolean,
    /** True whenever the list is not at the very top. */
    scrolled: Boolean,
    onQueryChange: (String) -> Unit,
    onToggleSearch: () -> Unit,
    onNewGroup: () -> Unit,
    onAddContact: () -> Unit,
    onOpenContacts: () -> Unit,
    onOpenDevices: () -> Unit,
    onOpenSettings: () -> Unit,
    /**
     * Disables the pairing entries. Left visible rather than hidden: a menu that
     * silently loses items reads as a bug, and the banner above already says
     * why they cannot be used.
     */
    revoked: Boolean,
) {
    var menuOpen by remember { mutableStateOf(false) }
    val focusRequester = remember { FocusRequester() }

    LaunchedEffect(searching) {
        if (searching) focusRequester.requestFocus()
    }

    // surfaceContainer rather than an elevation overlay: an overlay only lightens,
    // so it says nothing in light mode. This role is defined as a step *away*
    // from the background in whichever direction the theme runs - darker on
    // light, lighter on dark - which is exactly the cue wanted here.
    val container by animateColorAsState(
        targetValue = if (scrolled) {
            MaterialTheme.colorScheme.surfaceContainer
        } else {
            MaterialTheme.colorScheme.surface
        },
        label = "topBarContainer",
    )

    TopAppBar(
        colors = TopAppBarDefaults.topAppBarColors(containerColor = container),
        title = {
            if (searching) {
                TextField(
                    value = query,
                    onValueChange = onQueryChange,
                    singleLine = true,
                    placeholder = { Text(stringResource(R.string.threads_search_hint)) },
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                    // The field has to read as part of the app bar rather than
                    // as a form control dropped into it.
                    colors = TextFieldDefaults.colors(
                        focusedContainerColor = Color.Transparent,
                        unfocusedContainerColor = Color.Transparent,
                        disabledContainerColor = Color.Transparent,
                        focusedIndicatorColor = Color.Transparent,
                        unfocusedIndicatorColor = Color.Transparent,
                    ),
                    modifier = Modifier
                        .fillMaxWidth()
                        .focusRequester(focusRequester),
                )
            } else {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    // Sized from the title's line height, so the mark occupies
                    // exactly the box the text already claims and the bar does
                    // not grow. scaledDp keeps it tracking the system font
                    // scale; the cap stops it outgrowing the 64dp bar at the
                    // accessibility sizes.
                    val logoHeight = MaterialTheme.typography.titleLarge.lineHeight
                        .scaledDp(TITLE_LOGO_MAX)
                    Image(
                        painter = painterResource(R.drawable.ic_bounce_logo),
                        // Decorative: the word "Bounce" is right beside it, and
                        // announcing the mark as well just says it twice.
                        contentDescription = null,
                        modifier = Modifier.size(
                            width = logoHeight * TITLE_LOGO_ASPECT,
                            height = logoHeight,
                        ),
                    )
                    Spacer(Modifier.width(8.dp))
                    Text(stringResource(R.string.app_name))
                }
            }
        },
        actions = {
            IconButton(onClick = onToggleSearch) {
                Icon(
                    imageVector = if (searching) Icons.Filled.Close else Icons.Filled.Search,
                    contentDescription = stringResource(
                        if (searching) R.string.threads_search_close else R.string.threads_search,
                    ),
                )
            }

            Box {
                IconButton(onClick = { menuOpen = true }) {
                    Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.threads_more))
                }
                DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                    // First, because on a network with no user directory it is
                    // the only way to acquire anyone to talk to - a new install
                    // has an empty contact list and nothing else here helps.
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.add_contact)) },
                        enabled = !revoked,
                        onClick = {
                            menuOpen = false
                            onAddContact()
                        },
                    )
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.contacts_title)) },
                        onClick = {
                            menuOpen = false
                            onOpenContacts()
                        },
                    )
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.new_group)) },
                        onClick = {
                            menuOpen = false
                            onNewGroup()
                        },
                    )
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.devices_title)) },
                        enabled = !revoked,
                        onClick = {
                            menuOpen = false
                            onOpenDevices()
                        },
                    )
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.settings_title)) },
                        onClick = {
                            menuOpen = false
                            onOpenSettings()
                        },
                    )
                }
            }
        },
    )
}

@Composable
private fun ThreadRowItem(
    row: ThreadRow,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
    onAccept: () -> Unit,
    onDecline: () -> Unit,
) {
    Column(
        Modifier
            .fillMaxWidth()
            .combinedClickable(onClick = onClick, onLongClick = onLongClick)
            .padding(horizontal = 16.dp, vertical = 10.dp),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.heightIn(min = 56.dp),
        ) {
            if (row.isSelf) {
                NoteToSelfAvatar(row)
            } else {
                Avatar(
                    fileIds = row.imageIds,
                    fallbackId = row.id,
                    fallbackName = row.name,
                    size = 52.dp,
                    online = row.online,
                )
            }

            Spacer(Modifier.width(12.dp))

            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = row.name,
                        style = MaterialTheme.typography.titleMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    if (row.muted) {
                        Spacer(Modifier.width(6.dp))
                        Icon(
                            imageVector = Icons.Outlined.NotificationsOff,
                            contentDescription = stringResource(R.string.thread_muted),
                            tint = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.size(16.dp),
                        )
                    }
                }

                Spacer(Modifier.height(2.dp))
                PreviewLine(row)
            }

            Spacer(Modifier.width(12.dp))

            Column(horizontalAlignment = Alignment.End) {
                Text(
                    text = row.time,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (row.unreadCount > 0) {
                    Spacer(Modifier.height(6.dp))
                    UnreadBadge(row.unreadCount)
                } else if (row.outgoingStatus != null) {
                    // Only when there is no unread badge to show. The two are
                    // mutually exclusive by construction anyway - unread counts
                    // incoming messages, and this is non-null only when the
                    // newest message is outgoing - but a thread can hold both an
                    // older unread message and our newer reply, and in that case
                    // the badge is the more useful of the two.
                    Spacer(Modifier.height(6.dp))
                    // Larger than in a bubble: here it stands alone under the
                    // timestamp rather than sharing a row with other glyphs, and
                    // it is the only status the row shows.
                    DeliveryStatusIcon(
                        state = row.outgoingStatus,
                        mutedColor = MaterialTheme.colorScheme.onSurfaceVariant,
                        size = ROW_STATUS_SIZE.scaledDp(ROW_STATUS_MAX),
                    )
                }
            }
        }

        if (row.pendingInvite) {
            Spacer(Modifier.height(8.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = onAccept) { Text(stringResource(R.string.thread_accept)) }
                OutlinedButton(onClick = onDecline) { Text(stringResource(R.string.thread_decline)) }
            }
        }
    }
}

/**
 * Your own avatar with a note badge overhanging the circle.
 *
 * A note-to-self thread is your own name and your own picture, which is
 * indistinguishable from a contact at a glance. The badge deliberately breaks
 * the avatar's outline - sitting fully inside it would read as part of the
 * photo rather than as a marker on it.
 */
@Composable
private fun NoteToSelfAvatar(row: ThreadRow) {
    val badge = 22.dp
    Box(
        // Room for the overhang, so the badge is not clipped by the row and the
        // avatar still lines up with every other row's.
        modifier = Modifier.size(52.dp),
        contentAlignment = Alignment.Center,
    ) {
        Avatar(
            fileIds = row.imageIds,
            fallbackId = row.id,
            fallbackName = row.name,
            size = 52.dp,
        )
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .align(Alignment.BottomEnd)
                .offset(x = 5.dp, y = 5.dp)
                .size(badge)
                .clip(CircleShape)
                .background(MaterialTheme.colorScheme.surface)
                .border(1.dp, MaterialTheme.colorScheme.surface, CircleShape),
        ) {
            Text(
                text = stringResource(R.string.thread_note_to_self_badge),
                style = MaterialTheme.typography.labelSmall,
            )
        }
    }
}

@Composable
private fun PreviewLine(row: ThreadRow) {
    // Typing outranks both the draft and the last message: it is the only one of
    // the three that is happening right now.
    val typingNames = row.typingNames
    if (typingNames.isNotEmpty()) {
        Text(
            text = when {
                !row.isGroup -> stringResource(R.string.thread_typing)
                typingNames.size == 1 -> stringResource(R.string.conv_typing_one, typingNames[0])
                typingNames.size == 2 ->
                    stringResource(R.string.conv_typing_two, typingNames[0], typingNames[1])
                else -> stringResource(R.string.conv_typing_many)
            },
            style = MaterialTheme.typography.bodyMedium,
            fontStyle = FontStyle.Italic,
            color = MaterialTheme.colorScheme.primary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        return
    }

    val draft = row.draft
    val systemEvent = row.systemEvent

    // A status change is the row's whole line, italic and with no author prefix,
    // matching the desktop client's setLastAction. A draft still wins over it -
    // unsent text the user typed is the more urgent thing to surface.
    if (draft == null && systemEvent != null) {
        Text(
            text = systemEventText(systemEvent),
            style = MaterialTheme.typography.bodyMedium,
            fontStyle = FontStyle.Italic,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        return
    }

    val draftPrefix = stringResource(R.string.thread_draft_prefix)
    val draftStyle = SpanStyle(
        color = MaterialTheme.colorScheme.error,
        fontStyle = FontStyle.Italic,
        fontWeight = FontWeight.SemiBold,
    )

    Text(
        text = buildAnnotatedString {
            if (draft != null) {
                withStyle(draftStyle) { append(draftPrefix) }
                append(draft)
            } else {
                append(row.preview)
            }
        },
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        maxLines = 2,
        overflow = TextOverflow.Ellipsis,
    )
}

@Composable
private fun UnreadBadge(count: Int) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .heightIn(min = 20.dp)
            .widthIn(min = 20.dp)
            .background(BounceTheme.colors.unreadBadge, CircleShape)
            .padding(horizontal = 6.dp, vertical = 2.dp),
    ) {
        Text(
            // The badge cannot be allowed to widen the row without bound.
            text = if (count > 999) stringResource(R.string.thread_unread_overflow) else "$count",
            style = MaterialTheme.typography.labelSmall,
            color = BounceTheme.colors.onUnreadBadge,
        )
    }
}

@Composable
private fun Banner(icon: ImageVector, text: String) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.errorContainer)
            .padding(horizontal = 16.dp, vertical = 8.dp),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onErrorContainer,
            modifier = Modifier.size(18.dp),
        )
        Spacer(Modifier.width(8.dp))
        Text(
            text = text,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onErrorContainer,
        )
    }
}

/** @param progress null during the two indeterminate phases (starting, preparing). */
@Composable
private fun SyncProgress(progress: Float?) {
    Column(
        Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 8.dp),
    ) {
        Text(
            text = stringResource(R.string.threads_syncing),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(6.dp))
        if (progress == null) {
            LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
        } else {
            LinearProgressIndicator(
                progress = { progress.coerceIn(0f, 1f) },
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

// --- long-press actions ------------------------------------------------------

@Composable
private fun ThreadActionsSheet(
    row: ThreadRow,
    viewModel: ThreadListViewModel,
    onDismiss: () -> Unit,
) {
    var muteDialog by remember { mutableStateOf(false) }
    var confirm by remember { mutableStateOf<Confirmation?>(null) }
    val sheetState = rememberModalBottomSheetState()

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(Modifier.navigationBarsPadding()) {
            Text(
                text = row.name,
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(horizontal = 24.dp, vertical = 8.dp),
            )
            HorizontalDivider()

            if (row.muted) {
                ActionRow(stringResource(R.string.thread_action_unmute)) {
                    viewModel.unmute(row)
                    onDismiss()
                }
            } else {
                ActionRow(stringResource(R.string.thread_action_mute)) { muteDialog = true }
            }

            ActionRow(stringResource(R.string.thread_action_clear_history)) {
                confirm = Confirmation.ClearHistory
            }

            if (row.isGroup) {
                ActionRow(stringResource(R.string.thread_action_leave)) { confirm = Confirmation.Remove }
                ActionRow(stringResource(R.string.thread_action_block_group)) { confirm = Confirmation.Block }
            } else {
                ActionRow(stringResource(R.string.thread_action_hide)) { confirm = Confirmation.Remove }
                if (row.blocked) {
                    ActionRow(stringResource(R.string.thread_action_unblock_user)) {
                        viewModel.unblock(row)
                        onDismiss()
                    }
                } else {
                    ActionRow(stringResource(R.string.thread_action_block_user)) {
                        confirm = Confirmation.Block
                    }
                }
            }

            Spacer(Modifier.height(8.dp))
        }
    }

    if (muteDialog) {
        MuteDialog(
            onDismiss = { muteDialog = false },
            onPick = { duration ->
                viewModel.mute(row, duration)
                muteDialog = false
                onDismiss()
            },
        )
    }

    confirm?.let { pending ->
        ConfirmDialog(
            confirmation = pending,
            isGroup = row.isGroup,
            onDismiss = { confirm = null },
            onConfirm = {
                when (pending) {
                    Confirmation.ClearHistory -> viewModel.clearHistory(row)
                    Confirmation.Block -> viewModel.block(row)
                    Confirmation.Remove -> viewModel.removeThread(row)
                }
                confirm = null
                onDismiss()
            },
        )
    }
}

/** Every destructive action warns first; only the copy differs. */
private enum class Confirmation { ClearHistory, Block, Remove }

@Composable
private fun ActionRow(text: String, onClick: () -> Unit) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyLarge,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 16.dp),
    )
}

@Composable
private fun MuteDialog(onDismiss: () -> Unit, onPick: (MuteDuration) -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.mute_title)) },
        text = {
            Column {
                MuteDuration.entries.forEach { duration ->
                    Text(
                        text = stringResource(duration.label),
                        style = MaterialTheme.typography.bodyLarge,
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable { onPick(duration) }
                            .padding(vertical = 14.dp),
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) }
        },
    )
}

@Composable
private fun ConfirmDialog(
    confirmation: Confirmation,
    isGroup: Boolean,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
) {
    val (title, body) = when (confirmation) {
        Confirmation.ClearHistory ->
            R.string.confirm_clear_history_title to R.string.confirm_clear_history_body

        Confirmation.Block -> if (isGroup) {
            R.string.confirm_block_group_title to R.string.confirm_block_group_body
        } else {
            R.string.confirm_block_user_title to R.string.confirm_block_user_body
        }

        Confirmation.Remove -> if (isGroup) {
            R.string.confirm_leave_title to R.string.confirm_leave_body
        } else {
            R.string.confirm_hide_title to R.string.confirm_hide_body
        }
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(title)) },
        text = { Text(stringResource(body)) },
        confirmButton = {
            TextButton(onClick = onConfirm) {
                Text(
                    text = stringResource(R.string.action_confirm),
                    color = MaterialTheme.colorScheme.error,
                )
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) }
        },
    )
}

// --- new chat ----------------------------------------------------------------

/**
 * The FAB destination: known contacts plus ways to start something new.
 *
 * Rendered as a bottom-anchored surface inside a navigation dialog destination,
 * so it sits over the still-visible inbox and Back dismisses it for free.
 */
@Composable
fun NewChatSheet(
    onOpenThread: (String) -> Unit,
    onNewGroup: () -> Unit,
    onAddContact: () -> Unit,
    onDismiss: () -> Unit,
    viewModel: ThreadListViewModel = viewModel(),
) {
    val contacts by viewModel.contacts.collectAsStateWithLifecycle()
    // Straight from the repository rather than viewModel.state: that flow
    // rebuilds every thread row on a minute tick, and this sheet only needs the
    // one flag.
    val revoked by ChatRepository.deviceRevoked.collectAsStateWithLifecycle()
    val scrimInteraction = remember { MutableInteractionSource() }

    Box(
        contentAlignment = Alignment.BottomCenter,
        modifier = Modifier
            .fillMaxSize()
            // The dialog window is full-screen, so tap-outside-to-dismiss is ours to wire.
            .clickable(
                interactionSource = scrimInteraction,
                indication = null,
                onClick = onDismiss,
            ),
    ) {
        Surface(
            shape = RoundedCornerShape(topStart = 28.dp, topEnd = 28.dp),
            tonalElevation = 3.dp,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Column(Modifier.navigationBarsPadding()) {
                Text(
                    text = stringResource(R.string.new_chat),
                    style = MaterialTheme.typography.titleMedium,
                    modifier = Modifier.padding(horizontal = 24.dp, vertical = 16.dp),
                )

                EntryRow(
                    icon = Icons.Outlined.PersonAdd,
                    label = stringResource(R.string.add_contact),
                    enabled = !revoked,
                ) {
                    onDismiss()
                    onAddContact()
                }
                EntryRow(Icons.Outlined.Group, stringResource(R.string.new_group)) {
                    onDismiss()
                    onNewGroup()
                }

                HorizontalDivider()

                if (contacts.isEmpty()) {
                    Text(
                        text = stringResource(R.string.new_chat_no_contacts),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(24.dp),
                    )
                } else {
                    LazyColumn(Modifier.heightIn(max = 400.dp)) {
                        items(contacts, key = { it.id }) { contact ->
                            ContactRowItem(contact) {
                                viewModel.openContact(contact.id) { threadId ->
                                    onDismiss()
                                    onOpenThread(threadId)
                                }
                            }
                        }
                    }
                }

                Spacer(Modifier.height(8.dp))
            }
        }
    }
}

@Composable
private fun EntryRow(
    icon: ImageVector,
    label: String,
    enabled: Boolean = true,
    onClick: () -> Unit,
) {
    // Dimmed to the same degree Material disables a control with, so a row that
    // cannot be tapped does not read as one that simply failed to respond.
    val tint = MaterialTheme.colorScheme.primary
    val alpha = if (enabled) 1f else DISABLED_ALPHA
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = enabled, onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 14.dp),
    ) {
        Icon(icon, contentDescription = null, tint = tint.copy(alpha = alpha))
        Spacer(Modifier.width(16.dp))
        Text(
            text = label,
            style = MaterialTheme.typography.bodyLarge,
            color = LocalContentColor.current.copy(alpha = alpha),
        )
    }
}

@Composable
private fun ContactRowItem(contact: ContactRow, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 8.dp),
    ) {
        Avatar(
            fileIds = contact.imageIds,
            fallbackId = contact.id,
            fallbackName = contact.name,
            size = 40.dp,
        )
        Spacer(Modifier.width(16.dp))
        Text(
            text = contact.name,
            style = MaterialTheme.typography.bodyLarge,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * Delivery glyph in an inbox row. Larger than the bubble's, because here it
 * stands alone under the timestamp rather than sharing a row with other glyphs.
 * Capped for the same reason as the bubble's - see META_ICON_MAX there.
 */
/** Material's disabled-content opacity, applied by hand where no `enabled` flag exists. */
private const val DISABLED_ALPHA = 0.38f

private val ROW_STATUS_SIZE = 15.sp
private val ROW_STATUS_MAX = 22.dp

/**
 * The logo's own proportions, from `ic_bounce_logo.xml` (96dp x 92dp). Applied
 * explicitly because the drawable is wider than it is tall: constraining only
 * the height in a Row would let the painter keep its intrinsic width and
 * letterbox the mark inside it.
 */
private const val TITLE_LOGO_ASPECT = 96f / 92f

/**
 * Ceiling for the title mark. titleLarge's 28sp line height lands at 28dp for
 * most people; the 64dp app bar has room to let that grow with the font scale,
 * but not to 56dp.
 */
private val TITLE_LOGO_MAX = 36.dp
