package chat.bounce.ui.settings

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
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Edit
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
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import chat.bounce.BuildConfig
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.engine.EngineClient
import chat.bounce.engine.EngineHolder
import chat.bounce.ui.components.Avatar
import chat.bounce.ui.components.AvatarPickerButton
import chat.bounce.ui.components.LabelledRetentionPicker
import chat.bounce.ui.components.rememberAvatarPicker
import chat.bounce.ui.theme.ThemeChoice
import chat.bounce.ui.theme.ThemePreference
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

/**
 * Global preferences.
 *
 * Every control writes through to the engine immediately - there is no Save
 * button, matching the desktop client - and the visible value comes back from
 * `ChatRepository.settings`, so a change made on another device shows up here
 * without any extra plumbing.
 *
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    val settings by ChatRepository.settings.collectAsStateWithLifecycle()
    val profile by ChatRepository.profile.collectAsStateWithLifecycle()
    // Local to this device, so it comes from SharedPreferences rather than the
    // engine - see ThemePreference for why it does not sync.
    val themeChoice by ThemePreference.choice.collectAsStateWithLifecycle()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.settings_title)) },
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
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState()),
        ) {
            ProfileSection(
                id = profile?.id.orEmpty(),
                name = profile?.displayName.orEmpty(),
                images = profile?.images ?: emptyList(),
                scope = scope,
            )

            HorizontalDivider()
            SectionTitle(stringResource(R.string.settings_disappearing))
            Column(Modifier.padding(horizontal = 24.dp)) {
                Text(
                    text = stringResource(R.string.settings_disappearing_help),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(16.dp))
                LabelledRetentionPicker(
                    label = stringResource(R.string.settings_default_dm_retention),
                    value = settings.defaultDMRetention,
                    onValueChange = { value -> scope.engine { it.setNewDMRetention(value) } },
                )
                Spacer(Modifier.height(16.dp))
                LabelledRetentionPicker(
                    label = stringResource(R.string.settings_default_group_retention),
                    value = settings.defaultGroupRetention,
                    onValueChange = { value -> scope.engine { it.setNewGroupRetention(value) } },
                )
                Spacer(Modifier.height(16.dp))
            }

            HorizontalDivider()
            SectionTitle(stringResource(R.string.settings_privacy))
            SwitchRow(
                title = stringResource(R.string.settings_read_receipts),
                subtitle = stringResource(R.string.settings_read_receipts_help),
                checked = settings.defaultSendReadReceipts,
                onCheckedChange = { value -> scope.engine { it.setReadReceiptsByDefault(value) } },
            )
            SwitchRow(
                title = stringResource(R.string.settings_typing_indicators),
                subtitle = stringResource(R.string.settings_typing_indicators_help),
                checked = settings.defaultSendTypingIndicators,
                onCheckedChange = { value ->
                    scope.engine { it.setTypingIndicatorsByDefault(value) }
                },
            )

            Column(Modifier.padding(horizontal = 24.dp, vertical = 8.dp)) {
                Text(
                    text = stringResource(R.string.settings_auto_join),
                    style = MaterialTheme.typography.bodyLarge,
                )
                Spacer(Modifier.height(6.dp))
                AutoJoinPicker(
                    value = settings.autoJoinGroups,
                    onValueChange = { value -> scope.engine { it.setAutoJoinGroups(value) } },
                )
            }

            HorizontalDivider()
            SectionTitle(stringResource(R.string.settings_new_group_defaults))
            SwitchRow(
                title = stringResource(R.string.new_group_restrict_user_management),
                checked = settings.newGroupRestrictUserManagement,
                onCheckedChange = { value ->
                    scope.engine { it.setNewGroupRestrictUserManagement(value) }
                },
            )
            SwitchRow(
                title = stringResource(R.string.new_group_restrict_group_edits),
                checked = settings.newGroupRestrictGroupEdits,
                onCheckedChange = { value ->
                    scope.engine { it.setNewGroupRestrictGroupEdits(value) }
                },
            )
            SwitchRow(
                title = stringResource(R.string.new_group_restrict_posting),
                checked = settings.newGroupRestrictPosting,
                onCheckedChange = { value -> scope.engine { it.setNewGroupRestrictPosting(value) } },
            )

            HorizontalDivider()
            SectionTitle(stringResource(R.string.settings_appearance))
            Column(Modifier.padding(horizontal = 24.dp)) {
                Text(
                    text = stringResource(R.string.settings_theme),
                    style = MaterialTheme.typography.bodyLarge,
                )
                Spacer(Modifier.height(6.dp))
                ThemePicker(
                    value = themeChoice,
                    onValueChange = { ThemePreference.set(it) },
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    text = stringResource(R.string.settings_theme_help),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            HorizontalDivider()
            SectionTitle(stringResource(R.string.settings_about))
            Column(Modifier.padding(horizontal = 24.dp)) {
                Text(
                    text = stringResource(R.string.settings_about_body),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(12.dp))
                Text(
                    text = stringResource(R.string.settings_version, BuildConfig.VERSION_NAME),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.height(32.dp))
        }
    }
}

@Composable
private fun ProfileSection(
    id: String,
    name: String,
    images: List<String>,
    scope: CoroutineScope,
) {
    var editing by remember { mutableStateOf(false) }
    var draft by remember(name) { mutableStateOf(name) }
    var avatar by remember { mutableStateOf<ByteArray?>(null) }

    val pickAvatar = rememberAvatarPicker { bytes ->
        if (bytes != null) {
            avatar = bytes
            scope.engine { it.updateProfileImage(bytes) }
        }
    }

    // Once the engine has stored the new image it comes back as a normal
    // attachment on the profile, so the local preview is dropped to avoid two
    // sources of truth for the same picture.
    LaunchedEffect(images) { avatar = null }

    Row(
        modifier = Modifier.fillMaxWidth().padding(24.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        AvatarPickerButton(
            picked = avatar,
            size = 72.dp,
            contentDescription = stringResource(R.string.settings_change_photo),
            onClick = pickAvatar,
        ) {
            Avatar(fileIds = images, fallbackId = id, fallbackName = name, size = 72.dp)
        }

        Box(Modifier.weight(1f).padding(start = 16.dp)) {
            if (editing) {
                OutlinedTextField(
                    value = draft,
                    onValueChange = { draft = it.trimStart().take(MAX_NAME_LENGTH) },
                    label = { Text(stringResource(R.string.settings_your_name)) },
                    singleLine = true,
                    trailingIcon = {
                        Row {
                            IconButton(onClick = { draft = name; editing = false }) {
                                Icon(
                                    Icons.Filled.Close,
                                    contentDescription = stringResource(R.string.action_cancel),
                                )
                            }
                            IconButton(
                                enabled = draft.isNotBlank() && draft != name,
                                onClick = {
                                    val wanted = draft.trim()
                                    editing = false
                                    scope.engine { it.updateProfileName(wanted) }
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
                    modifier = Modifier.fillMaxWidth().clickable { editing = true },
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        text = name,
                        style = MaterialTheme.typography.titleMedium,
                        modifier = Modifier.weight(1f),
                    )
                    Icon(
                        Icons.Filled.Edit,
                        contentDescription = stringResource(R.string.settings_edit_name),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

@Composable
private fun AutoJoinPicker(value: Int, onValueChange: (Int) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    val labels = listOf(
        stringResource(R.string.settings_auto_join_known),
        stringResource(R.string.settings_auto_join_never),
        stringResource(R.string.settings_auto_join_always),
    )

    Box {
        OutlinedButton(onClick = { expanded = true }, modifier = Modifier.fillMaxWidth()) {
            Text(labels.getOrElse(value) { labels[0] }, modifier = Modifier.weight(1f))
            Icon(Icons.Filled.ArrowDropDown, contentDescription = null)
        }
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            labels.forEachIndexed { index, label ->
                DropdownMenuItem(
                    text = { Text(label) },
                    onClick = {
                        expanded = false
                        if (index != value) onValueChange(index)
                    },
                )
            }
        }
    }
}

@Composable
private fun ThemePicker(value: ThemeChoice, onValueChange: (ThemeChoice) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    // Paired with the enum rather than indexed, so reordering the entries cannot
    // silently relabel them the way AutoJoinPicker's positional list would.
    val options = listOf(
        ThemeChoice.SYSTEM to stringResource(R.string.settings_theme_system),
        ThemeChoice.LIGHT to stringResource(R.string.settings_theme_light),
        ThemeChoice.DARK to stringResource(R.string.settings_theme_dark),
    )
    val label = options.firstOrNull { it.first == value }?.second ?: options[0].second

    Box {
        OutlinedButton(onClick = { expanded = true }, modifier = Modifier.fillMaxWidth()) {
            Text(label, modifier = Modifier.weight(1f))
            Icon(Icons.Filled.ArrowDropDown, contentDescription = null)
        }
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            options.forEach { (choice, text) ->
                DropdownMenuItem(
                    text = { Text(text) },
                    onClick = {
                        expanded = false
                        if (choice != value) onValueChange(choice)
                    },
                )
            }
        }
    }
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
private fun SwitchRow(
    title: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    subtitle: String? = null,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable { onCheckedChange(!checked) }
            .padding(horizontal = 24.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f).padding(end = 16.dp)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            if (subtitle != null) {
                Text(
                    text = subtitle,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        Switch(checked = checked, onCheckedChange = onCheckedChange)
    }
}

/**
 * Fire-and-forget engine write.
 *
 * Settings changes are echoed back through `SetSettings`, so the UI never waits
 * on the call; failures leave the switch showing the value the engine still
 * holds, which is the truthful outcome.
 */
private fun CoroutineScope.engine(block: suspend (EngineClient) -> Unit) {
    launch {
        val client = EngineHolder.client ?: return@launch
        runCatching { block(client) }
    }
}

private const val MAX_NAME_LENGTH = 128
