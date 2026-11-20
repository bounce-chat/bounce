package chat.bounce.ui.details

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
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
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import chat.bounce.R
import chat.bounce.ui.components.Avatar
import chat.bounce.ui.components.AvatarPickerButton
import chat.bounce.ui.components.LabelledRetentionPicker
import chat.bounce.ui.components.rememberAvatarPicker
import chat.bounce.ui.threads.MuteDuration

/**
 * Everything about one group: who is in it, what it remembers, and who is
 * allowed to change any of that.
 *
 * The screen is a projection of [GroupInfoViewModel]'s state and never decides a
 * permission for itself - the engine's rules are already resolved into the
 * canEdit/canManageUsers/canPromote flags, so a control here is enabled exactly
 * when the update behind it would be accepted.
 *
 * [onOpenContact] is not called for the local user: there is no useful contact
 * card for yourself, and the profile lives in Settings.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun GroupInfoScreen(
    groupId: String,
    onBack: () -> Unit,
    onOpenContact: (String) -> Unit,
) {
    val factory = remember(groupId) { GroupInfoViewModel.factory(groupId) }
    val viewModel: GroupInfoViewModel = viewModel(key = groupId, factory = factory)
    val state by viewModel.state.collectAsStateWithLifecycle()

    var confirming by remember { mutableStateOf<GroupConfirmation?>(null) }
    var inviting by remember { mutableStateOf(false) }

    // Deleting, blocking and leaving all drop the group from the repository, and
    // so does another admin deleting it while this screen is open. There is
    // nothing left to describe, so the screen closes itself rather than showing
    // a husk the user has to back out of.
    LaunchedEffect(state.loading, state.exists) {
        if (!state.loading && !state.exists) onBack()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.group_info_title)) },
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
        if (!state.exists) {
            Box(
                modifier = Modifier.padding(padding).fillMaxSize(),
                contentAlignment = Alignment.Center,
            ) {
                // Blank while the first snapshot is still being combined; the
                // message is only for a group that is genuinely gone.
                if (!state.loading) {
                    Text(
                        text = stringResource(R.string.group_info_missing),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            return@Scaffold
        }

        LazyColumn(
            modifier = Modifier.padding(padding).fillMaxSize(),
            contentPadding = PaddingValues(bottom = 32.dp),
        ) {
            item {
                GroupHeader(
                    state = state,
                    onRename = viewModel::rename,
                    onPickImage = viewModel::setImage,
                )
            }

            if (state.isPendingInvite) {
                item {
                    PendingInviteCard(
                        invitedByName = state.invitedByName,
                        onAccept = viewModel::acceptInvite,
                        onDecline = { confirming = GroupConfirmation.DeclineInvite },
                        onBlock = { confirming = GroupConfirmation.BlockGroup },
                    )
                    HorizontalDivider()
                    SectionTitle(stringResource(R.string.group_info_members))
                }
                // Seeing who is already in the group is most of what an invite
                // decision rests on, so the list stays - without any of the
                // management the engine would refuse from a non-member.
                items(state.members, key = { "member-${it.id}" }) { member ->
                    MemberRow(
                        member = member,
                        onOpen = if (member.isSelf) null else ({ onOpenContact(member.id) }),
                        onPromote = {},
                        onDemote = {},
                        onRemove = {},
                    )
                }
                return@LazyColumn
            }

            item {
                HorizontalDivider()
                SectionTitle(stringResource(R.string.group_info_notifications))
                MuteRow(
                    muted = state.muted,
                    onMute = { duration -> viewModel.setMutedUntil(duration.mutedUntil()) },
                    onUnmute = { viewModel.setMutedUntil(0L) },
                )

                HorizontalDivider()
                SectionTitle(stringResource(R.string.settings_disappearing))
                Column(Modifier.padding(horizontal = 24.dp)) {
                    LabelledRetentionPicker(
                        label = stringResource(R.string.settings_default_group_retention),
                        value = state.retention,
                        enabled = state.canEdit,
                        onValueChange = viewModel::setRetention,
                    )
                }

                HorizontalDivider()
                SectionTitle(stringResource(R.string.group_info_privacy))
                Column(Modifier.padding(horizontal = 24.dp)) {
                    Text(
                        text = stringResource(R.string.group_info_privacy_help),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(16.dp))
                    PreferencePicker(
                        label = stringResource(R.string.settings_read_receipts),
                        value = state.readReceipts,
                        defaultEnabled = state.defaultReadReceipts,
                        onValueChange = viewModel::setReadReceipts,
                    )
                    Spacer(Modifier.height(16.dp))
                    PreferencePicker(
                        label = stringResource(R.string.settings_typing_indicators),
                        value = state.typingIndicators,
                        defaultEnabled = state.defaultTypingIndicators,
                        onValueChange = viewModel::setTypingIndicators,
                    )
                }

                HorizontalDivider(Modifier.padding(top = 20.dp))
                MembersHeader(
                    canInvite = state.canManageUsers,
                    onInvite = { inviting = true },
                )
                if (!state.canManageUsers && state.amMember) {
                    HelpText(stringResource(R.string.group_info_management_restricted))
                }
            }

            items(state.members, key = { "member-${it.id}" }) { member ->
                MemberRow(
                    member = member,
                    onOpen = if (member.isSelf) null else ({ onOpenContact(member.id) }),
                    onPromote = { viewModel.promote(member.id) },
                    onDemote = { viewModel.demote(member.id) },
                    onRemove = {
                        confirming = GroupConfirmation.RemoveMember(member.id, member.name)
                    },
                )
            }

            if (state.invites.isNotEmpty()) {
                item { SectionTitle(stringResource(R.string.group_info_invites)) }
                items(state.invites, key = { "invite-${it.id}" }) { invite ->
                    InviteeRow(
                        invite = invite,
                        canRevoke = state.canManageUsers,
                        onRevoke = {
                            confirming = GroupConfirmation.RevokeInvite(invite.id, invite.name)
                        },
                    )
                }
            }

            item {
                HorizontalDivider(Modifier.padding(top = 12.dp))
                SectionTitle(stringResource(R.string.group_info_permissions))
                // Shown to everyone, because how the group is configured is worth
                // knowing even when you cannot change it.
                if (!state.amAdmin) HelpText(stringResource(R.string.group_info_permissions_help))
                SwitchRow(
                    title = stringResource(R.string.new_group_restrict_user_management),
                    checked = state.restrictUserManagement,
                    enabled = state.amAdmin,
                    onCheckedChange = viewModel::setRestrictUserManagement,
                )
                SwitchRow(
                    title = stringResource(R.string.new_group_restrict_group_edits),
                    checked = state.restrictGroupEdits,
                    enabled = state.amAdmin,
                    onCheckedChange = viewModel::setRestrictGroupEdits,
                )
                SwitchRow(
                    title = stringResource(R.string.new_group_restrict_posting),
                    checked = state.restrictPosting,
                    enabled = state.amAdmin,
                    onCheckedChange = viewModel::setRestrictPosting,
                )

                HorizontalDivider(Modifier.padding(top = 12.dp))
                SectionTitle(stringResource(R.string.group_info_danger_zone))
                DangerRow(
                    title = stringResource(R.string.thread_action_clear_history),
                    enabled = state.canEdit,
                    onClick = { confirming = GroupConfirmation.ClearHistory },
                )
                DangerRow(
                    title = stringResource(R.string.thread_action_block_group),
                    onClick = { confirming = GroupConfirmation.BlockGroup },
                )
                DangerRow(
                    title = stringResource(R.string.thread_action_leave),
                    enabled = state.amMember,
                    onClick = { confirming = GroupConfirmation.LeaveGroup },
                )
                // Delete is admin-only in the engine, and unlike the rest of the
                // danger zone there is no honest disabled state for it: it is not
                // a thing a member may do at all.
                if (state.amAdmin) {
                    DangerRow(
                        title = stringResource(R.string.group_info_delete),
                        onClick = { confirming = GroupConfirmation.DeleteGroup },
                    )
                }
            }
        }
    }

    confirming?.let { pending ->
        ConfirmDialog(
            confirmation = pending,
            onDismiss = { confirming = null },
            onConfirm = {
                confirming = null
                pending.perform(viewModel)
            },
        )
    }

    if (inviting) {
        InvitePeopleDialog(
            candidates = state.invitable,
            onInvite = { userId ->
                inviting = false
                viewModel.invite(userId)
            },
            onDismiss = { inviting = false },
        )
    }
}

// --- header -----------------------------------------------------------------

@Composable
private fun GroupHeader(
    state: GroupInfoState,
    onRename: (String) -> Unit,
    onPickImage: (ByteArray) -> Unit,
) {
    var picked by remember { mutableStateOf<ByteArray?>(null) }
    var editing by remember { mutableStateOf(false) }
    var draft by remember(state.name) { mutableStateOf(state.name) }

    val pickImage = rememberAvatarPicker { bytes ->
        if (bytes != null) {
            picked = bytes
            onPickImage(bytes)
        }
    }

    // Once stored, the photo comes back on the group as an ordinary attachment,
    // so the local preview is dropped to avoid two sources of truth for it.
    LaunchedEffect(state.imageIds) { picked = null }

    // Another admin can restrict edits while this screen is open; a text field
    // whose save button can never fire again would be a trap.
    LaunchedEffect(state.canEdit) { if (!state.canEdit) editing = false }

    Column(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        // The same circle either way; only the camera badge and the tap target
        // depend on whether this user may change the photo.
        val avatar: @Composable () -> Unit = {
            Avatar(
                fileIds = state.imageIds,
                fallbackId = state.groupId,
                fallbackName = state.name,
                size = AVATAR_SIZE,
            )
        }
        if (state.canEdit) {
            AvatarPickerButton(
                picked = picked,
                size = AVATAR_SIZE,
                contentDescription = stringResource(R.string.group_info_change_photo),
                onClick = pickImage,
                placeholder = avatar,
            )
        } else {
            avatar()
        }

        Spacer(Modifier.height(16.dp))

        if (editing) {
            OutlinedTextField(
                value = draft,
                onValueChange = { draft = it.trimStart().take(MAX_GROUP_NAME_LENGTH) },
                label = { Text(stringResource(R.string.group_info_name)) },
                singleLine = true,
                trailingIcon = {
                    Row {
                        IconButton(onClick = { draft = state.name; editing = false }) {
                            Icon(
                                Icons.Filled.Close,
                                contentDescription = stringResource(R.string.action_cancel),
                            )
                        }
                        IconButton(
                            enabled = draft.isNotBlank() && draft.trim() != state.name,
                            onClick = {
                                val wanted = draft.trim()
                                editing = false
                                onRename(wanted)
                            },
                        ) {
                            Icon(
                                Icons.Filled.Check,
                                contentDescription = stringResource(R.string.action_save),
                            )
                        }
                    }
                },
                modifier = Modifier.fillMaxWidth(),
            )
        } else {
            Row(
                modifier = if (state.canEdit) {
                    Modifier.clickable { editing = true }.padding(4.dp)
                } else {
                    Modifier.padding(4.dp)
                },
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(text = state.name, style = MaterialTheme.typography.titleLarge)
                if (state.canEdit) {
                    Icon(
                        Icons.Filled.Edit,
                        contentDescription = stringResource(R.string.group_info_edit_name),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(start = 8.dp).size(18.dp),
                    )
                }
            }
        }

        Spacer(Modifier.height(8.dp))
        Text(
            text = pluralStringResource(
                R.plurals.group_info_member_count,
                state.memberCount,
                state.memberCount,
            ),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        createdLine(state)?.let { line ->
            Text(
                text = line,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
        }
        if (!state.canEdit && state.amMember && !state.isPendingInvite) {
            Spacer(Modifier.height(8.dp))
            Text(
                text = stringResource(R.string.group_info_edits_restricted),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
        }
    }
}

/**
 * "Created by Ana on Mar 4, 2025", degrading to whichever half is known. Null
 * when the group carries neither, which is the case for one synced from a peer
 * that never sent its creation frame.
 */
@Composable
private fun createdLine(state: GroupInfoState): String? {
    val who = when {
        state.createdBySelf -> stringResource(R.string.conv_sys_you_object)
        state.createdByName.isNotEmpty() -> state.createdByName
        else -> null
    }
    val date = state.createdAtLabel.ifEmpty { null }
    return when {
        who != null && date != null -> stringResource(R.string.group_info_created_by_on, who, date)
        who != null -> stringResource(R.string.group_info_created_by, who)
        date != null -> stringResource(R.string.group_info_created_on, date)
        else -> null
    }
}

@Composable
private fun PendingInviteCard(
    invitedByName: String,
    onAccept: () -> Unit,
    onDecline: () -> Unit,
    onBlock: () -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = if (invitedByName.isEmpty()) {
                stringResource(R.string.group_info_invited)
            } else {
                stringResource(R.string.group_info_invited_by, invitedByName)
            },
            style = MaterialTheme.typography.bodyMedium,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(16.dp))
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Button(onClick = onAccept, modifier = Modifier.weight(1f)) {
                Text(stringResource(R.string.thread_accept))
            }
            OutlinedButton(onClick = onDecline, modifier = Modifier.weight(1f)) {
                Text(stringResource(R.string.thread_decline))
            }
        }
        // Declining leaves the door open for another invite; blocking is the
        // only answer that closes it permanently.
        TextButton(
            onClick = onBlock,
            colors = ButtonDefaults.textButtonColors(
                contentColor = MaterialTheme.colorScheme.error,
            ),
        ) {
            Text(stringResource(R.string.thread_action_block_group))
        }
    }
}

// --- members and invites ----------------------------------------------------

@Composable
private fun MembersHeader(canInvite: Boolean, onInvite: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = 24.dp, end = 12.dp, top = 20.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = stringResource(R.string.group_info_members),
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.weight(1f),
        )
        TextButton(enabled = canInvite, onClick = onInvite) {
            Icon(Icons.Filled.PersonAdd, contentDescription = null, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(6.dp))
            Text(stringResource(R.string.group_info_invite_people))
        }
    }
}

@Composable
private fun MemberRow(
    member: GroupMemberRow,
    onOpen: (() -> Unit)?,
    onPromote: () -> Unit,
    onDemote: () -> Unit,
    onRemove: () -> Unit,
) {
    var expanded by remember { mutableStateOf(false) }

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .then(if (onOpen != null) Modifier.clickable(onClick = onOpen) else Modifier)
            .padding(start = 24.dp, end = 12.dp, top = 8.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Avatar(
            fileIds = member.imageIds,
            fallbackId = member.id,
            fallbackName = member.name,
            size = 40.dp,
            // Your own presence is not information; everyone else's is.
            online = if (member.isSelf) null else member.online,
        )
        Column(Modifier.weight(1f).padding(horizontal = 16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(text = member.name, style = MaterialTheme.typography.bodyLarge)
                if (member.isAdmin) {
                    Text(
                        text = stringResource(R.string.group_info_admin),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.padding(start = 8.dp),
                    )
                }
            }
            if (member.isSelf) {
                Text(
                    text = stringResource(R.string.conv_sys_you),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }

        if (member.hasActions) {
            Box {
                IconButton(onClick = { expanded = true }) {
                    Icon(
                        Icons.Filled.MoreVert,
                        contentDescription = stringResource(
                            R.string.group_info_member_options,
                            member.name,
                        ),
                    )
                }
                DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                    if (member.canPromote) {
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.group_info_promote)) },
                            onClick = {
                                expanded = false
                                onPromote()
                            },
                        )
                    }
                    if (member.canDemote) {
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.group_info_demote)) },
                            onClick = {
                                expanded = false
                                onDemote()
                            },
                        )
                    }
                    if (member.canRemove) {
                        DropdownMenuItem(
                            text = {
                                Text(
                                    text = stringResource(R.string.group_info_remove_member),
                                    color = MaterialTheme.colorScheme.error,
                                )
                            },
                            onClick = {
                                expanded = false
                                onRemove()
                            },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun InviteeRow(invite: GroupInviteRow, canRevoke: Boolean, onRevoke: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = 24.dp, end = 12.dp, top = 8.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Avatar(
            fileIds = invite.imageIds,
            fallbackId = invite.id,
            fallbackName = invite.name,
            size = 40.dp,
        )
        Text(
            text = invite.name,
            style = MaterialTheme.typography.bodyLarge,
            modifier = Modifier.weight(1f).padding(horizontal = 16.dp),
        )
        TextButton(
            enabled = canRevoke,
            onClick = onRevoke,
            colors = ButtonDefaults.textButtonColors(
                contentColor = MaterialTheme.colorScheme.error,
            ),
        ) {
            Text(stringResource(R.string.group_info_action_revoke))
        }
    }
}

@Composable
private fun InvitePeopleDialog(
    candidates: List<GroupInviteRow>,
    onInvite: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.group_info_invite_people)) },
        text = {
            if (candidates.isEmpty()) {
                Text(
                    text = stringResource(R.string.group_info_invite_empty),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            } else {
                LazyColumn(Modifier.heightIn(max = INVITE_LIST_MAX_HEIGHT)) {
                    items(candidates, key = { it.id }) { candidate ->
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clickable { onInvite(candidate.id) }
                                .padding(vertical = 8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Avatar(
                                fileIds = candidate.imageIds,
                                fallbackId = candidate.id,
                                fallbackName = candidate.name,
                                size = 36.dp,
                            )
                            Text(
                                text = candidate.name,
                                style = MaterialTheme.typography.bodyLarge,
                                modifier = Modifier.padding(start = 16.dp),
                            )
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) }
        },
    )
}

// --- rows -------------------------------------------------------------------

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
private fun HelpText(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 24.dp, end = 24.dp, bottom = 8.dp),
    )
}

@Composable
private fun MuteRow(muted: Boolean, onMute: (MuteDuration) -> Unit, onUnmute: () -> Unit) {
    var expanded by remember { mutableStateOf(false) }

    Box {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clickable { expanded = true }
                .padding(horizontal = 24.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = stringResource(R.string.group_info_mute),
                style = MaterialTheme.typography.bodyLarge,
                modifier = Modifier.weight(1f).padding(end = 16.dp),
            )
            Text(
                text = stringResource(
                    if (muted) R.string.thread_muted else R.string.group_info_not_muted
                ),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            if (muted) {
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.thread_action_unmute)) },
                    onClick = {
                        expanded = false
                        onUnmute()
                    },
                )
            }
            MuteDuration.entries.forEach { duration ->
                DropdownMenuItem(
                    text = { Text(stringResource(duration.label)) },
                    onClick = {
                        expanded = false
                        onMute(duration)
                    },
                )
            }
        }
    }
}

/**
 * The per-thread override of a global privacy default. "Default" carries the
 * current global value in its label so choosing it is not a guess.
 */
@Composable
private fun PreferencePicker(
    label: String,
    value: GroupInfoPreference,
    defaultEnabled: Boolean,
    onValueChange: (GroupInfoPreference) -> Unit,
) {
    var expanded by remember { mutableStateOf(false) }

    Column(Modifier.fillMaxWidth()) {
        Text(text = label, style = MaterialTheme.typography.bodyLarge)
        Spacer(Modifier.height(6.dp))
        Box {
            OutlinedButton(onClick = { expanded = true }, modifier = Modifier.fillMaxWidth()) {
                Text(preferenceLabel(value, defaultEnabled), modifier = Modifier.weight(1f))
                Icon(Icons.Filled.ArrowDropDown, contentDescription = null)
            }
            DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                GroupInfoPreference.entries.forEach { option ->
                    DropdownMenuItem(
                        text = { Text(preferenceLabel(option, defaultEnabled)) },
                        onClick = {
                            expanded = false
                            if (option != value) onValueChange(option)
                        },
                    )
                }
            }
        }
    }
}

@Composable
private fun preferenceLabel(
    preference: GroupInfoPreference,
    defaultEnabled: Boolean,
): String = when (preference) {
    GroupInfoPreference.Default -> stringResource(
        if (defaultEnabled) R.string.group_info_pref_default_on
        else R.string.group_info_pref_default_off
    )

    GroupInfoPreference.On -> stringResource(R.string.group_info_pref_on)
    GroupInfoPreference.Off -> stringResource(R.string.group_info_pref_off)
}

@Composable
private fun SwitchRow(
    title: String,
    checked: Boolean,
    enabled: Boolean,
    onCheckedChange: (Boolean) -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = enabled) { onCheckedChange(!checked) }
            .padding(horizontal = 24.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.bodyLarge,
            color = if (enabled) {
                MaterialTheme.colorScheme.onSurface
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
            modifier = Modifier.weight(1f).padding(end = 16.dp),
        )
        Switch(checked = checked, enabled = enabled, onCheckedChange = onCheckedChange)
    }
}

@Composable
private fun DangerRow(title: String, onClick: () -> Unit, enabled: Boolean = true) {
    TextButton(
        onClick = onClick,
        enabled = enabled,
        colors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.error),
        modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp),
    ) {
        Text(text = title, modifier = Modifier.weight(1f), textAlign = TextAlign.Start)
    }
}

// --- confirmations ----------------------------------------------------------

/**
 * Nothing here is undoable, and several are irreversible for everyone in the
 * group rather than just for us, so each one is routed through the same
 * confirmation instead of firing on tap.
 */
private sealed interface GroupConfirmation {
    data object ClearHistory : GroupConfirmation
    data object BlockGroup : GroupConfirmation
    data object LeaveGroup : GroupConfirmation
    data object DeleteGroup : GroupConfirmation
    data object DeclineInvite : GroupConfirmation
    data class RemoveMember(val id: String, val name: String) : GroupConfirmation
    data class RevokeInvite(val id: String, val name: String) : GroupConfirmation
}

private fun GroupConfirmation.perform(viewModel: GroupInfoViewModel) = when (this) {
    GroupConfirmation.ClearHistory -> viewModel.clearHistory()
    GroupConfirmation.BlockGroup -> viewModel.blockGroup()
    GroupConfirmation.LeaveGroup -> viewModel.leaveGroup()
    GroupConfirmation.DeleteGroup -> viewModel.deleteGroup()
    GroupConfirmation.DeclineInvite -> viewModel.declineInvite()
    is GroupConfirmation.RemoveMember -> viewModel.removeMember(id)
    is GroupConfirmation.RevokeInvite -> viewModel.revokeInvite(id)
}

private data class ConfirmCopy(val title: String, val body: String, val action: String)

@Composable
private fun confirmCopyFor(confirmation: GroupConfirmation): ConfirmCopy = when (confirmation) {
    GroupConfirmation.ClearHistory -> ConfirmCopy(
        title = stringResource(R.string.confirm_clear_history_title),
        body = stringResource(R.string.confirm_clear_history_body),
        action = stringResource(R.string.group_info_action_clear),
    )

    GroupConfirmation.BlockGroup -> ConfirmCopy(
        title = stringResource(R.string.confirm_block_group_title),
        body = stringResource(R.string.confirm_block_group_body),
        action = stringResource(R.string.group_info_action_block),
    )

    GroupConfirmation.LeaveGroup -> ConfirmCopy(
        title = stringResource(R.string.confirm_leave_title),
        body = stringResource(R.string.confirm_leave_body),
        action = stringResource(R.string.group_info_action_leave),
    )

    GroupConfirmation.DeleteGroup -> ConfirmCopy(
        title = stringResource(R.string.group_info_delete_title),
        body = stringResource(R.string.group_info_delete_body),
        action = stringResource(R.string.group_info_action_delete),
    )

    GroupConfirmation.DeclineInvite -> ConfirmCopy(
        title = stringResource(R.string.group_info_decline_title),
        body = stringResource(R.string.group_info_decline_body),
        action = stringResource(R.string.group_info_action_decline),
    )

    is GroupConfirmation.RemoveMember -> ConfirmCopy(
        title = stringResource(R.string.group_info_remove_member_title, confirmation.name),
        body = stringResource(R.string.group_info_remove_member_body),
        action = stringResource(R.string.group_info_action_remove),
    )

    is GroupConfirmation.RevokeInvite -> ConfirmCopy(
        title = stringResource(R.string.group_info_revoke_title, confirmation.name),
        body = stringResource(R.string.group_info_revoke_body),
        action = stringResource(R.string.group_info_action_revoke),
    )
}

@Composable
private fun ConfirmDialog(
    confirmation: GroupConfirmation,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
) {
    val copy = confirmCopyFor(confirmation)

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(copy.title) },
        text = { Text(copy.body) },
        confirmButton = {
            TextButton(
                onClick = onConfirm,
                colors = ButtonDefaults.textButtonColors(
                    contentColor = MaterialTheme.colorScheme.error,
                ),
            ) {
                Text(copy.action)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) }
        },
    )
}

private val AVATAR_SIZE = 96.dp
private val INVITE_LIST_MAX_HEIGHT = 320.dp

private const val MAX_GROUP_NAME_LENGTH = 128
