package chat.bounce.ui.contacts

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.NotificationsOff
import androidx.compose.material.icons.outlined.PersonAdd
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.ui.components.Avatar
import chat.bounce.ui.components.EmptyState

/**
 * Everyone this device knows.
 *
 * There is no global user directory in Bounce - an identity is an onion address
 * that only its owner can hand over - so this list is the whole address book,
 * and the natural place to start a conversation with someone who is not already
 * in the inbox.
 *
 * @param onOpenThread the conversation. The DM is opened on the engine first
 *   (see [ContactsViewModel.openContact]), because most rows here have never had
 *   a thread at all.
 * @param onOpenProfile the per-contact detail screen, which is the only route to
 *   unblocking someone.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ContactsScreen(
    onBack: () -> Unit,
    onOpenThread: (String) -> Unit,
    onOpenProfile: (String) -> Unit,
    onAddContact: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: ContactsViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    // A revoked device can never complete the pairing handshake that adding a
    // contact requires, so the entry points to it are disabled rather than
    // left to fail three minutes later in awaitNetworkOnline.
    val revoked by ChatRepository.deviceRevoked.collectAsStateWithLifecycle()

    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.contacts_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.action_back),
                        )
                    }
                },
                actions = {
                    IconButton(onClick = onAddContact, enabled = !revoked) {
                        Icon(
                            Icons.Outlined.PersonAdd,
                            contentDescription = stringResource(R.string.add_contact),
                        )
                    }
                },
            )
        },
    ) { padding ->
        Column(Modifier.padding(padding)) {
            SearchField(query = state.query, onQueryChange = viewModel::setQuery)

            state.noteToSelf?.let { self ->
                ContactRowItem(
                    contact = self,
                    title = stringResource(R.string.contacts_note_to_self),
                    subtitle = stringResource(R.string.contacts_note_to_self_subtitle),
                    onClick = { viewModel.openContact(self, onOpenThread) },
                    // Pinned outside the scrolling list, so it stays reachable
                    // however far down the address book the user is.
                    onInfo = null,
                )
                HorizontalDivider()
            }

            when {
                state.sections.isNotEmpty() -> ContactList(
                    sections = state.sections,
                    onOpen = { viewModel.openContact(it, onOpenThread) },
                    onOpenProfile = onOpenProfile,
                )

                // A non-empty address book with nothing visible can only mean the
                // search matched nobody.
                state.hasAnyContacts -> EmptyState(
                    icon = Icons.Filled.Search,
                    title = stringResource(R.string.contacts_no_results),
                )

                else -> EmptyState(
                    icon = Icons.Outlined.PersonAdd,
                    title = stringResource(R.string.contacts_empty_title),
                    description = stringResource(R.string.contacts_empty_body),
                    action = {
                        Button(onClick = onAddContact, enabled = !revoked) {
                            Text(stringResource(R.string.add_contact))
                        }
                    },
                )
            }
        }
    }
}

@Composable
private fun ContactList(
    sections: List<ContactSection>,
    onOpen: (ContactEntry) -> Unit,
    onOpenProfile: (String) -> Unit,
) {
    LazyColumn(Modifier.fillMaxSize()) {
        sections.forEach { section ->
            item(key = "section:${section.letter ?: BLOCKED_SECTION_KEY}") {
                SectionHeader(
                    section.letter ?: stringResource(R.string.contacts_blocked_section),
                )
            }
            items(section.contacts, key = { it.id }) { contact ->
                ContactRowItem(
                    contact = contact,
                    title = contact.displayName,
                    // The name they chose for themselves, plain. Wrapping it in
                    // "Calls themselves X" editorialises a line that is only
                    // ever shown when an alias is already hiding it.
                    subtitle = contact.selfDeclaredName,
                    onClick = { onOpen(contact) },
                    onInfo = { onOpenProfile(contact.id) },
                )
            }
        }
    }
}

@Composable
private fun SearchField(query: String, onQueryChange: (String) -> Unit) {
    OutlinedTextField(
        value = query,
        onValueChange = onQueryChange,
        singleLine = true,
        placeholder = { Text(stringResource(R.string.contacts_search_hint)) },
        leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
        trailingIcon = {
            if (query.isNotEmpty()) {
                IconButton(onClick = { onQueryChange("") }) {
                    Icon(
                        Icons.Filled.Close,
                        contentDescription = stringResource(R.string.contacts_search_clear),
                    )
                }
            }
        },
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 8.dp),
    )
}

@Composable
private fun SectionHeader(label: String) {
    Text(
        text = label,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier
            .fillMaxWidth()
            // Opaque rather than transparent: rows scroll behind the heading.
            .background(MaterialTheme.colorScheme.surface)
            .padding(start = 16.dp, end = 16.dp, top = 12.dp, bottom = 4.dp),
    )
}

/**
 * @param title what to call this row, which is not always the contact's name -
 *   the note-to-self row is titled for what it is.
 * @param onInfo null hides the trailing button for rows with no profile to open.
 */
@Composable
private fun ContactRowItem(
    contact: ContactEntry,
    title: String,
    subtitle: String?,
    onClick: () -> Unit,
    onInfo: (() -> Unit)?,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(start = 16.dp, end = 4.dp, top = 8.dp, bottom = 8.dp),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            // Blocked contacts stay in the list because the profile behind this
            // row is the only place to undo it, but they must not read as someone
            // the user is in touch with.
            modifier = Modifier
                .weight(1f)
                .alpha(if (contact.blocked) BLOCKED_ALPHA else 1f),
        ) {
            Avatar(
                fileIds = contact.imageIds,
                fallbackId = contact.id,
                fallbackName = contact.displayName,
                size = 44.dp,
                // A presence dot on your own row would only ever say "online".
                online = if (contact.self) null else contact.online,
            )

            Spacer(Modifier.width(12.dp))

            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = title,
                        style = MaterialTheme.typography.titleMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    if (contact.muted) {
                        Spacer(Modifier.width(6.dp))
                        Icon(
                            imageVector = Icons.Outlined.NotificationsOff,
                            contentDescription = stringResource(R.string.thread_muted),
                            tint = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.size(16.dp),
                        )
                    }
                    if (contact.blocked) {
                        Spacer(Modifier.width(8.dp))
                        BlockedBadge()
                    }
                }

                if (subtitle != null) {
                    Spacer(Modifier.height(2.dp))
                    Text(
                        text = subtitle,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }

        if (onInfo != null) {
            IconButton(onClick = onInfo) {
                Icon(
                    imageVector = Icons.Outlined.Info,
                    contentDescription = stringResource(R.string.contacts_details, title),
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
private fun BlockedBadge() {
    Text(
        text = stringResource(R.string.contacts_blocked_badge),
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onErrorContainer,
        modifier = Modifier
            .background(MaterialTheme.colorScheme.errorContainer, RoundedCornerShape(4.dp))
            .padding(horizontal = 6.dp, vertical = 2.dp),
    )
}

/** Contact ids are UUIDs, so no section key can collide with one. */
private const val BLOCKED_SECTION_KEY = "blocked"
private const val BLOCKED_ALPHA = 0.55f
