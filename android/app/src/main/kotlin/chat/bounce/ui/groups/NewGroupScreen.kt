package chat.bounce.ui.groups

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Groups
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.engine.EngineHolder
import chat.bounce.engine.NewGroup
import chat.bounce.engine.User
import chat.bounce.ui.components.Avatar
import chat.bounce.ui.components.AvatarPickerButton
import chat.bounce.ui.components.LabelledRetentionPicker
import chat.bounce.ui.components.rememberAvatarPicker
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/**
 * Create a group and invite the first members.
 *
 * Only existing contacts can be invited: a group invitation carries an onion
 * address, and the engine will not hand one out for somebody this profile has
 * never paired with.
 *
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewGroupScreen(
    onBack: () -> Unit,
    onCreated: (String) -> Unit,
) {
    val scope = rememberCoroutineScope()

    val settings by ChatRepository.settings.collectAsStateWithLifecycle()
    val profile by ChatRepository.profile.collectAsStateWithLifecycle()
    val users by ChatRepository.users.collectAsStateWithLifecycle()

    var name by remember { mutableStateOf("") }
    var avatar by remember { mutableStateOf<ByteArray?>(null) }
    val selected = remember { mutableStateListOf<String>() }

    var retention by remember { mutableStateOf<Long?>(null) }
    var restrictUserManagement by remember { mutableStateOf<Boolean?>(null) }
    var restrictGroupEdits by remember { mutableStateOf<Boolean?>(null) }
    var restrictPosting by remember { mutableStateOf<Boolean?>(null) }

    // Settings arrive asynchronously, so the toggles seed themselves the first
    // time real values show up rather than latching onto the empty defaults.
    LaunchedEffect(settings) {
        if (retention == null) retention = settings.defaultGroupRetention
        if (restrictUserManagement == null) {
            restrictUserManagement = settings.newGroupRestrictUserManagement
        }
        if (restrictGroupEdits == null) restrictGroupEdits = settings.newGroupRestrictGroupEdits
        if (restrictPosting == null) restrictPosting = settings.newGroupRestrictPosting
    }

    var creating by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf(false) }

    val pickAvatar = rememberAvatarPicker { bytes -> if (bytes != null) avatar = bytes }

    val selfId = profile?.id
    val invitable = remember(users, selfId) {
        users.values
            .filter { it.id != selfId && !it.blocked }
            .sortedBy { it.displayName.lowercase() }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.new_group)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.action_back),
                        )
                    }
                },
                actions = {
                    TextButton(
                        enabled = name.isNotBlank() && !creating,
                        onClick = {
                            creating = true
                            failed = false
                            scope.launch {
                                val id = createGroupAndAwaitId(
                                    NewGroup(
                                        name = name.trim(),
                                        initialInvites = selected.toList(),
                                        retention = retention ?: 0L,
                                        restrictUserManagement = restrictUserManagement == true,
                                        restrictGroupEdits = restrictGroupEdits == true,
                                        restrictPosting = restrictPosting == true,
                                    ),
                                    avatar ?: ByteArray(0),
                                )
                                creating = false
                                if (id != null) onCreated(id) else failed = true
                            }
                        },
                    ) {
                        Text(stringResource(R.string.new_group_create))
                    }
                },
            )
        },
    ) { padding ->
        LazyColumn(
            modifier = Modifier.padding(padding).fillMaxSize(),
            contentPadding = PaddingValues(bottom = 32.dp),
        ) {
            item {
                Column(
                    modifier = Modifier.fillMaxWidth().padding(24.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    AvatarPickerButton(
                        picked = avatar,
                        size = 96.dp,
                        contentDescription = stringResource(R.string.new_group_photo),
                        onClick = pickAvatar,
                    ) {
                        Box(
                            modifier = Modifier
                                .fillMaxSize()
                                .background(MaterialTheme.colorScheme.surfaceVariant, CircleShape),
                            contentAlignment = Alignment.Center,
                        ) {
                            Icon(
                                imageVector = Icons.Filled.Groups,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.size(44.dp),
                            )
                        }
                    }
                    Spacer(Modifier.height(20.dp))
                    OutlinedTextField(
                        value = name,
                        onValueChange = { name = it.trimStart().take(MAX_GROUP_NAME_LENGTH) },
                        label = { Text(stringResource(R.string.new_group_name)) },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }

            item {
                SectionHeader(
                    title = stringResource(R.string.new_group_members),
                    trailing = if (selected.isEmpty()) {
                        null
                    } else {
                        stringResource(R.string.new_group_selected, selected.size)
                    },
                )
            }

            if (invitable.isEmpty()) {
                item {
                    Text(
                        text = stringResource(R.string.new_group_no_contacts),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        textAlign = TextAlign.Center,
                        modifier = Modifier.fillMaxWidth().padding(24.dp),
                    )
                }
            } else {
                items(invitable, key = { it.id }) { user ->
                    ContactRow(
                        user = user,
                        checked = user.id in selected,
                        onToggle = {
                            if (user.id in selected) selected.remove(user.id)
                            else selected.add(user.id)
                        },
                    )
                }
            }

            item {
                HorizontalDivider(Modifier.padding(vertical = 8.dp))
                SectionHeader(title = stringResource(R.string.new_group_advanced))
                Column(Modifier.padding(horizontal = 24.dp)) {
                    LabelledRetentionPicker(
                        label = stringResource(R.string.settings_disappearing),
                        value = retention ?: 0L,
                        onValueChange = { retention = it },
                    )
                    Spacer(Modifier.height(8.dp))
                    ToggleRow(
                        title = stringResource(R.string.new_group_restrict_user_management),
                        checked = restrictUserManagement == true,
                        onCheckedChange = { restrictUserManagement = it },
                    )
                    ToggleRow(
                        title = stringResource(R.string.new_group_restrict_group_edits),
                        checked = restrictGroupEdits == true,
                        onCheckedChange = { restrictGroupEdits = it },
                    )
                    ToggleRow(
                        title = stringResource(R.string.new_group_restrict_posting),
                        checked = restrictPosting == true,
                        onCheckedChange = { restrictPosting = it },
                    )
                }
            }

            if (creating || failed) {
                item {
                    Box(
                        modifier = Modifier.fillMaxWidth().padding(24.dp),
                        contentAlignment = Alignment.Center,
                    ) {
                        if (creating) {
                            CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                        } else {
                            Text(
                                text = stringResource(R.string.new_group_failed),
                                style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.error,
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun SectionHeader(title: String, trailing: String? = null) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.weight(1f),
        )
        if (trailing != null) {
            Text(
                text = trailing,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun ContactRow(user: User, checked: Boolean, onToggle: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onToggle)
            .padding(horizontal = 24.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Avatar(
            fileIds = user.images,
            fallbackId = user.id,
            fallbackName = user.displayName,
            size = 40.dp,
        )
        Text(
            text = user.displayName,
            style = MaterialTheme.typography.bodyLarge,
            modifier = Modifier.weight(1f).padding(horizontal = 16.dp),
        )
        Checkbox(checked = checked, onCheckedChange = { onToggle() })
    }
}

@Composable
private fun ToggleRow(title: String, checked: Boolean, onCheckedChange: (Boolean) -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.weight(1f).padding(end = 16.dp),
        )
        Switch(checked = checked, onCheckedChange = onCheckedChange)
    }
}

/**
 * Creates the group and resolves the UUID the engine assigned it.
 *
 * `createGroup` returns nothing; the group only becomes addressable when the
 * engine replays it back through `OpenNewGroupChat`, which the repository folds
 * into its group list. Waiting for the new ID to appear there is what lets the
 * caller navigate straight into the thread.
 */
private suspend fun createGroupAndAwaitId(newGroup: NewGroup, image: ByteArray): String? {
    val client = EngineHolder.client ?: return null
    val before = ChatRepository.groups.value.keys.toSet()
    val sent = runCatching { client.createGroup(newGroup, image) }
    if (sent.isFailure) return null

    // Bounded because the echo travels through the engine's own event loop; if
    // that stalls, the user gets an error instead of a spinner that never ends.
    return withTimeoutOrNull(GROUP_CREATE_TIMEOUT_MS) {
        ChatRepository.groups
            .first { groups -> groups.keys.any { it !in before } }
            .keys
            .first { it !in before }
    }
}

private const val GROUP_CREATE_TIMEOUT_MS = 30_000L

private const val MAX_GROUP_NAME_LENGTH = 128
