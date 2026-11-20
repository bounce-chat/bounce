package chat.bounce.ui.contacts

import android.util.Log
import androidx.compose.runtime.Immutable
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import chat.bounce.data.ChatRepository
import chat.bounce.engine.EngineClient
import chat.bounce.engine.EngineHolder
import chat.bounce.engine.User
import chat.bounce.goengine.Goengine
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import java.util.Locale

/** One person in the address book, formatted; the screen does no engine-shaped work. */
@Immutable
data class ContactEntry(
    val id: String,
    val displayName: String,
    /**
     * What they call themselves, and only when an alias is currently hiding it.
     * Null keeps the row to one line for the common case where the two agree.
     */
    val selfDeclaredName: String?,
    val imageIds: List<String>,
    val online: Boolean,
    val muted: Boolean,
    val blocked: Boolean,
    /** Our own profile - the note-to-self thread, which has no peer and no presence. */
    val self: Boolean = false,
)

/**
 * A run of contacts under one heading: an initial, or the blocked group.
 *
 * @param letter the initial these sort under, or null for the blocked group,
 *   which the screen labels from a string resource instead.
 */
@Immutable
data class ContactSection(
    val letter: String?,
    val contacts: List<ContactEntry>,
)

@Immutable
data class ContactsUiState(
    val query: String = "",
    val sections: List<ContactSection> = emptyList(),
    /** Pinned above the list. Null until the engine hands over a profile. */
    val noteToSelf: ContactEntry? = null,
    /** Distinguishes "nobody has been paired with" from "the search matched nothing". */
    val hasAnyContacts: Boolean = false,
)

/**
 * The whole address book.
 *
 * Bounce has no directory to look anyone up in, so [ChatRepository.users] is the
 * complete set of people this device can ever start a conversation with - which
 * is why this list, unlike the inbox, shows contacts with no thread yet and
 * keeps blocked ones visible.
 */
class ContactsViewModel : ViewModel() {

    private val _query = MutableStateFlow("")

    val state: StateFlow<ContactsUiState> = combine(
        ChatRepository.users,
        ChatRepository.profile,
        ChatRepository.onlineUsers,
        _query,
    ) { users, profile, online, query ->
        build(users, profile, online, query)
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), ContactsUiState())

    fun setQuery(value: String) {
        _query.value = value
    }

    /**
     * Opens the DM on the way to it. A contact reached from here usually has no
     * thread at all yet, and the engine only puts one in the list once it has
     * been told - it answers with SetDMState, which is what the inbox reacts to.
     */
    fun openContact(contact: ContactEntry, onOpened: (String) -> Unit) {
        onEngine {
            it.setOpenDM(contact.id, true)
            // Nothing to dial for either exception: the note-to-self thread lives
            // on our own devices, and a blocked contact must not be connected to
            // merely because their row was tapped.
            if (!contact.self && !contact.blocked) it.userConnectionDesired(contact.id)
        }
        onOpened(contact.id)
    }

    private fun build(
        users: Map<String, User>,
        profile: User?,
        online: Set<String>,
        query: String,
    ): ContactsUiState {
        val trimmed = query.trim()
        val myId = profile?.id.orEmpty()

        val matched = users.values.asSequence()
            .filter { it.id != myId }
            .filter { it.matches(trimmed) }
            .map { it.toEntry(online = it.id in online) }
            .sortedWith(byDisplayName)
            .toList()

        // Blocked contacts sort last as one labelled group rather than being
        // hidden: unblocking is only reachable through the profile, so dropping
        // them from the list would strand them.
        val (blocked, active) = matched.partition { it.blocked }

        val sections = buildList {
            // groupBy keeps insertion order, and `active` is already sorted, so
            // the headings come out in order for free.
            active.groupBy { it.displayName.sectionLetter() }
                .forEach { (letter, contacts) -> add(ContactSection(letter, contacts)) }
            if (blocked.isNotEmpty()) add(ContactSection(letter = null, contacts = blocked))
        }

        return ContactsUiState(
            query = query,
            sections = sections,
            // Filtered like everyone else: with a query typed, a row that does not
            // match reads as a bug rather than as a convenience.
            noteToSelf = profile?.takeIf { it.matches(trimmed) }
                ?.toEntry(online = false, self = true),
            hasAnyContacts = users.keys.any { it != myId },
        )
    }

    /**
     * Engine calls are blocking JNI and any of them can fail (offline, revoked
     * device). A failed action must not take the process down, and there is
     * nothing useful to retry: the engine re-emits the authoritative state either
     * way.
     */
    private fun onEngine(block: suspend (EngineClient) -> Unit) {
        val client = EngineHolder.client
        if (client == null) {
            Log.w(TAG, "engine not started, dropping action")
            return
        }
        viewModelScope.launch {
            runCatching { block(client) }.onFailure { Log.w(TAG, "engine call failed", it) }
        }
    }

    private companion object {
        const val TAG = "Contacts"
        const val STOP_TIMEOUT_MS = 5_000L
    }
}

/** Names sort case-insensitively; the id only breaks ties so the order is stable. */
private val byDisplayName = compareBy<ContactEntry>(
    { it.displayName.lowercase(Locale.getDefault()) },
    { it.id },
)

private fun User.matches(query: String): Boolean =
    query.isEmpty() ||
        displayName.contains(query, ignoreCase = true) ||
        name.contains(query, ignoreCase = true)

private fun User.toEntry(online: Boolean, self: Boolean = false) = ContactEntry(
    id = id,
    displayName = displayName,
    // displayName is the alias whenever there is one, so the name they broadcast
    // is only worth a second line while an alias is covering it up.
    selfDeclaredName = name.takeIf { it.isNotBlank() && alias.isNotBlank() && it != alias },
    imageIds = images,
    online = online,
    // A mute is stored as a deadline with a sentinel for "never unmute", so an
    // expired deadline has to read as unmuted.
    muted = state.mutedUntil == Goengine.MutedForever ||
        state.mutedUntil > System.currentTimeMillis() / 1000L,
    blocked = blocked,
    self = self,
)

/** The heading a name sorts under. Anything not starting with a letter shares one bucket. */
private fun String.sectionLetter(): String {
    val first = firstOrNull() ?: return OTHER_SECTION
    return if (first.isLetter()) first.uppercaseChar().toString() else OTHER_SECTION
}

private const val OTHER_SECTION = "#"
