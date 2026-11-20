package chat.bounce.ui.details

import androidx.annotation.StringRes
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import chat.bounce.R
import chat.bounce.ui.components.Avatar
import chat.bounce.ui.components.LabelledRetentionPicker
import chat.bounce.ui.theme.LocalBounceColors
import chat.bounce.ui.threads.MuteDuration

/**
 * Everything this device knows about one contact, and every per-conversation
 * setting for the DM with them.
 *
 * The distinction the screen exists to make visible is name vs alias: a contact's
 * name is theirs, is broadcast by them, and can change without warning, while the
 * alias is mine, local, and never leaves my own devices. The same split runs
 * through notes.
 *
 * @param onMessage opens the conversation. The DM is opened on the engine first
 *   (see [ContactProfileViewModel.openChat]) because it may be hidden or never
 *   have been opened at all.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ContactProfileScreen(
    userId: String,
    onBack: () -> Unit,
    onOpenGroup: (String) -> Unit,
    onMessage: (String) -> Unit,
) {
    val factory = remember(userId) { ContactProfileViewModel.factory(userId) }
    val viewModel: ContactProfileViewModel = viewModel(key = userId, factory = factory)
    val state by viewModel.state.collectAsStateWithLifecycle()

    var muting by remember { mutableStateOf(false) }
    var confirming by remember { mutableStateOf<ConfirmAction?>(null) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.contact_profile_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.action_back),
                        )
                    }
                },
            )
        },
    ) { padding ->
        when {
            state.loading -> Box(
                modifier = Modifier.padding(padding).fillMaxSize(),
                contentAlignment = Alignment.Center,
            ) {
                CircularProgressIndicator()
            }

            state.missing -> Box(
                modifier = Modifier.padding(padding).fillMaxSize(),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = stringResource(R.string.contact_not_found),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.padding(32.dp),
                )
            }

            else -> ProfileBody(
                state = state,
                viewModel = viewModel,
                onOpenGroup = onOpenGroup,
                onMessage = onMessage,
                onMute = { muting = true },
                onConfirm = { confirming = it },
                modifier = Modifier.padding(padding),
            )
        }
    }

    if (muting) {
        MuteDialog(
            onDismiss = { muting = false },
            onPick = { duration ->
                muting = false
                viewModel.setMutedUntil(duration.mutedUntil())
            },
        )
    }

    confirming?.let { action ->
        ConfirmDialog(
            action = action,
            onDismiss = { confirming = null },
            onConfirmed = {
                confirming = null
                when (action) {
                    ConfirmAction.ClearHistory -> viewModel.clearHistory()
                    ConfirmAction.Block -> viewModel.setBlocked(true)
                    ConfirmAction.Unblock -> viewModel.setBlocked(false)
                    ConfirmAction.HideChat -> viewModel.setChatOpen(false)
                }
            },
        )
    }
}

@Composable
private fun ProfileBody(
    state: ContactProfileUiState,
    viewModel: ContactProfileViewModel,
    onOpenGroup: (String) -> Unit,
    onMessage: (String) -> Unit,
    onMute: () -> Unit,
    onConfirm: (ConfirmAction) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState()),
    ) {
        Header(
            state = state,
            onMessage = {
                viewModel.openChat()
                onMessage(state.id)
            },
            onEditAlias = viewModel::editAlias,
            onSaveAlias = viewModel::saveAlias,
        )

        HorizontalDivider()
        SectionTitle(stringResource(R.string.contact_notes))
        Column(Modifier.padding(horizontal = 24.dp)) {
            OutlinedTextField(
                value = state.notesDraft,
                onValueChange = viewModel::editNotes,
                label = { Text(stringResource(R.string.contact_notes_label)) },
                supportingText = { Text(stringResource(R.string.contact_notes_help)) },
                minLines = 3,
                modifier = Modifier
                    .fillMaxWidth()
                    // Typing debounces; losing focus commits straight away, so a
                    // note is never left unsaved because the user moved on fast.
                    .onFocusChanged { if (!it.isFocused) viewModel.flushNotes() },
            )
        }

        HorizontalDivider()
        SectionTitle(stringResource(R.string.settings_disappearing))
        Column(Modifier.padding(horizontal = 24.dp)) {
            Text(
                text = stringResource(R.string.contact_retention_help),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(16.dp))
            LabelledRetentionPicker(
                label = stringResource(R.string.contact_retention_label),
                value = state.retention,
                onValueChange = viewModel::setRetention,
            )
        }

        HorizontalDivider()
        SectionTitle(stringResource(R.string.contact_notifications))
        ActionRow(
            title = stringResource(
                if (state.muted) R.string.thread_action_unmute else R.string.thread_action_mute
            ),
            subtitle = stringResource(
                if (state.muted) R.string.contact_muted_subtitle
                else R.string.contact_unmuted_subtitle
            ),
            onClick = { if (state.muted) viewModel.setMutedUntil(0L) else onMute() },
        )

        HorizontalDivider()
        SectionTitle(stringResource(R.string.settings_privacy))
        PrivacyChoiceRow(
            title = stringResource(R.string.settings_read_receipts),
            subtitle = stringResource(R.string.contact_read_receipts_help),
            value = state.readReceipts,
            default = state.defaultReadReceipts,
            onValueChange = viewModel::setReadReceipts,
        )
        PrivacyChoiceRow(
            title = stringResource(R.string.settings_typing_indicators),
            subtitle = stringResource(R.string.contact_typing_indicators_help),
            value = state.typingIndicators,
            default = state.defaultTypingIndicators,
            onValueChange = viewModel::setTypingIndicators,
        )

        HorizontalDivider()
        SectionTitle(stringResource(R.string.contact_groups_in_common))
        if (state.groupsInCommon.isEmpty()) {
            Text(
                text = stringResource(R.string.contact_groups_none),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 24.dp, vertical = 4.dp),
            )
        } else {
            state.groupsInCommon.forEach { group ->
                GroupRow(group = group, onClick = { onOpenGroup(group.id) })
            }
        }

        HorizontalDivider()
        SectionTitle(stringResource(R.string.contact_danger_zone))
        if (state.chatOpen) {
            ActionRow(
                title = stringResource(R.string.thread_action_hide),
                subtitle = stringResource(R.string.contact_hide_help),
                onClick = { onConfirm(ConfirmAction.HideChat) },
            )
        } else {
            ActionRow(
                title = stringResource(R.string.contact_show_chat),
                subtitle = stringResource(R.string.contact_show_help),
                onClick = { viewModel.setChatOpen(true) },
            )
        }
        ActionRow(
            title = stringResource(R.string.thread_action_clear_history),
            subtitle = stringResource(R.string.contact_clear_history_help),
            tint = MaterialTheme.colorScheme.error,
            onClick = { onConfirm(ConfirmAction.ClearHistory) },
        )
        if (state.blocked) {
            ActionRow(
                title = stringResource(R.string.thread_action_unblock_user),
                subtitle = stringResource(R.string.contact_unblock_help),
                onClick = { onConfirm(ConfirmAction.Unblock) },
            )
        } else {
            ActionRow(
                title = stringResource(R.string.thread_action_block_user),
                subtitle = stringResource(R.string.contact_block_help),
                tint = MaterialTheme.colorScheme.error,
                onClick = { onConfirm(ConfirmAction.Block) },
            )
        }

        Spacer(Modifier.height(32.dp))
    }
}

@Composable
private fun Header(
    state: ContactProfileUiState,
    onMessage: () -> Unit,
    onEditAlias: (String) -> Unit,
    onSaveAlias: () -> Unit,
) {
    val focusManager = LocalFocusManager.current
    val colors = LocalBounceColors.current

    // The heading is whichever name the rest of the app uses for this person, so
    // the line under it has to say where that name came from - otherwise an alias
    // and a self-declared name are indistinguishable.
    val unnamed = stringResource(R.string.contact_unnamed)
    val nameSource = if (state.alias.isNotBlank()) {
        stringResource(R.string.contact_header_alias_for, state.name.ifBlank { unnamed })
    } else {
        stringResource(R.string.contact_header_self_named)
    }

    Column(
        modifier = Modifier.fillMaxWidth().padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Avatar(
            fileIds = state.imageIds,
            fallbackId = state.id,
            fallbackName = state.displayName,
            size = 96.dp,
            online = state.online,
        )

        Spacer(Modifier.height(16.dp))
        Text(text = state.displayName, style = MaterialTheme.typography.headlineSmall)

        Spacer(Modifier.height(4.dp))
        Text(
            text = nameSource,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )

        Spacer(Modifier.height(4.dp))
        Text(
            text = stringResource(
                if (state.online) R.string.contact_online else R.string.contact_offline
            ),
            style = MaterialTheme.typography.bodySmall,
            color = if (state.online) colors.online else MaterialTheme.colorScheme.onSurfaceVariant,
        )

        if (state.blocked) {
            Spacer(Modifier.height(8.dp))
            Text(
                text = stringResource(R.string.contact_blocked_banner),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
                textAlign = TextAlign.Center,
            )
        }

        Spacer(Modifier.height(20.dp))
        Button(onClick = onMessage, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(R.string.contact_message))
        }

        Spacer(Modifier.height(20.dp))
        OutlinedTextField(
            value = state.aliasDraft,
            onValueChange = onEditAlias,
            label = { Text(stringResource(R.string.contact_alias_label)) },
            supportingText = { Text(stringResource(R.string.contact_alias_help)) },
            singleLine = true,
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
            keyboardActions = KeyboardActions(
                onDone = {
                    onSaveAlias()
                    focusManager.clearFocus()
                },
            ),
            trailingIcon = {
                // Only offered once the field differs from what the engine holds;
                // clearing the field and saving is how an alias is removed.
                if (state.aliasDirty) {
                    IconButton(
                        onClick = {
                            onSaveAlias()
                            focusManager.clearFocus()
                        }
                    ) {
                        Icon(
                            Icons.Filled.Check,
                            contentDescription = stringResource(R.string.contact_alias_save),
                        )
                    }
                }
            },
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun GroupRow(group: GroupInCommon, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Avatar(
            id = group.id,
            name = group.name,
            images = group.imageIds,
            size = 36.dp,
        )
        Text(
            text = group.name,
            style = MaterialTheme.typography.bodyLarge,
            modifier = Modifier.padding(start = 16.dp),
        )
    }
}

@Composable
private fun PrivacyChoiceRow(
    title: String,
    subtitle: String,
    value: ContactPrivacyChoice,
    default: Boolean,
    onValueChange: (ContactPrivacyChoice) -> Unit,
) {
    var expanded by remember { mutableStateOf(false) }

    Column(Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 8.dp)) {
        Text(title, style = MaterialTheme.typography.bodyLarge)
        Text(
            text = subtitle,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(6.dp))
        Box {
            OutlinedButton(onClick = { expanded = true }, modifier = Modifier.fillMaxWidth()) {
                Text(choiceLabel(value, default), modifier = Modifier.weight(1f))
                Icon(Icons.Filled.ArrowDropDown, contentDescription = null)
            }
            DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                ContactPrivacyChoice.entries.forEach { choice ->
                    DropdownMenuItem(
                        text = { Text(choiceLabel(choice, default)) },
                        onClick = {
                            expanded = false
                            if (choice != value) onValueChange(choice)
                        },
                    )
                }
            }
        }
    }
}

/** "Use default" names the account value, so the effect of picking it is visible. */
@Composable
private fun choiceLabel(choice: ContactPrivacyChoice, default: Boolean): String = when (choice) {
    ContactPrivacyChoice.Default -> stringResource(
        R.string.contact_override_default,
        stringResource(
            if (default) R.string.contact_override_on else R.string.contact_override_off
        ),
    )

    ContactPrivacyChoice.On -> stringResource(R.string.contact_override_on)
    ContactPrivacyChoice.Off -> stringResource(R.string.contact_override_off)
}

@Composable
private fun SectionTitle(title: String) {
    Text(
        text = title,
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 20.dp, bottom = 8.dp),
    )
}

@Composable
private fun ActionRow(
    title: String,
    subtitle: String,
    onClick: () -> Unit,
    tint: Color = Color.Unspecified,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 12.dp),
    ) {
        Text(text = title, style = MaterialTheme.typography.bodyLarge, color = tint)
        Text(
            text = subtitle,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

// --- dialogs ----------------------------------------------------------------

/**
 * The four actions that are not undoable by tapping the same row again, and so
 * are all confirmed the same way.
 *
 * [ConfirmAction.Block] borrows the shared block copy deliberately: the engine
 * recomputes the consensus of every group it shares with a blocked contact, which
 * takes us out of them, and the user has to be told that before it happens.
 */
private enum class ConfirmAction(
    @get:StringRes val title: Int,
    @get:StringRes val body: Int,
    @get:StringRes val action: Int,
    val destructive: Boolean,
) {
    ClearHistory(
        R.string.confirm_clear_history_title,
        R.string.confirm_clear_history_body,
        R.string.thread_action_clear_history,
        destructive = true,
    ),
    Block(
        R.string.confirm_block_user_title,
        R.string.confirm_block_user_body,
        R.string.thread_action_block_user,
        destructive = true,
    ),
    Unblock(
        R.string.contact_confirm_unblock_title,
        R.string.contact_confirm_unblock_body,
        R.string.thread_action_unblock_user,
        destructive = false,
    ),
    HideChat(
        R.string.confirm_hide_title,
        R.string.confirm_hide_body,
        R.string.thread_action_hide,
        destructive = false,
    ),
}

@Composable
private fun ConfirmDialog(
    action: ConfirmAction,
    onDismiss: () -> Unit,
    onConfirmed: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(action.title)) },
        text = { Text(stringResource(action.body)) },
        confirmButton = {
            TextButton(
                colors = if (action.destructive) {
                    ButtonDefaults.textButtonColors(
                        contentColor = MaterialTheme.colorScheme.error,
                    )
                } else {
                    ButtonDefaults.textButtonColors()
                },
                onClick = onConfirmed,
            ) {
                Text(stringResource(action.action))
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) }
        },
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
