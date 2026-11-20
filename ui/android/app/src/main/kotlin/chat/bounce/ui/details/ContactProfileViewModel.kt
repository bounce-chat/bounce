package chat.bounce.ui.details

import android.util.Log
import androidx.compose.runtime.Immutable
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import chat.bounce.data.ChatRepository
import chat.bounce.engine.EngineClient
import chat.bounce.engine.EngineHolder
import chat.bounce.engine.Group
import chat.bounce.engine.Settings
import chat.bounce.engine.User
import chat.bounce.goengine.Goengine
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import java.util.Locale

/** A group this contact and I are both in, as the "groups in common" list renders it. */
@Immutable
data class GroupInCommon(
    val id: String,
    val name: String,
    val imageIds: List<String>,
)

/**
 * One per-conversation privacy switch.
 *
 * The engine stores each of these as a *pair* - whether this conversation
 * overrides the account setting, and the value it uses if it does - so there are
 * three states to offer, not two. See chat/read_receipt.go: while [overrides] is
 * false the stored [enabled] flag is never consulted.
 */
enum class ContactPrivacyChoice {
    Default,
    On,
    Off;

    val overrides: Boolean get() = this != Default

    val enabled: Boolean get() = this == On

    companion object {
        fun of(override: Boolean, enabled: Boolean): ContactPrivacyChoice = when {
            !override -> Default
            enabled -> On
            else -> Off
        }
    }
}

@Immutable
data class ContactProfileUiState(
    val id: String = "",
    /** The engine has not handed over its snapshot yet; "not loaded" is not "not found". */
    val loading: Boolean = true,
    /** Loaded, but this UUID is not a contact on this device. */
    val missing: Boolean = false,
    /** What they call themselves. It changes when they change it. */
    val name: String = "",
    /** What I call them. Local, and never sent to them. */
    val alias: String = "",
    val displayName: String = "",
    val imageIds: List<String> = emptyList(),
    val online: Boolean = false,
    val blocked: Boolean = false,
    /** False once the DM has been hidden with SetOpenDM(false). */
    val chatOpen: Boolean = false,
    val retention: Long = 0,
    val muted: Boolean = false,
    val aliasDraft: String = "",
    /** True while the alias field holds something the engine has not been told about. */
    val aliasDirty: Boolean = false,
    val notesDraft: String = "",
    val readReceipts: ContactPrivacyChoice = ContactPrivacyChoice.Default,
    val typingIndicators: ContactPrivacyChoice = ContactPrivacyChoice.Default,
    /** The account-wide values, so "Use default" can say what the default is. */
    val defaultReadReceipts: Boolean = true,
    val defaultTypingIndicators: Boolean = true,
    val groupsInCommon: List<GroupInCommon> = emptyList(),
)

/**
 * State and engine plumbing for one contact's profile.
 *
 * Everything on this screen writes through to the engine immediately and is read
 * back from [ChatRepository], the same as the rest of the app: a DM's settings
 * are replicated across a profile's devices, so the engine's echo is the only
 * value worth rendering. The two free-text fields are the exception - they are
 * shadowed locally while being typed, because adopting a replicated value
 * mid-sentence would yank the text out from under the user.
 */
class ContactProfileViewModel(private val userId: String) : ViewModel() {

    // Null means "no local edit in flight": the field shows whatever the engine
    // last told us, including an alias or note set on another device.
    private val aliasDraft = MutableStateFlow<String?>(null)
    private val notesDraft = MutableStateFlow<String?>(null)

    private var notesSaveJob: Job? = null
    private var lastNotesWritten: String? = null

    /** onCleared() runs after viewModelScope is cancelled, so a pending save needs its own scope. */
    private val teardownScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    private val drafts = combine(aliasDraft, notesDraft) { alias, notes -> Drafts(alias, notes) }

    val state: StateFlow<ContactProfileUiState> = combine(
        ChatRepository.users,
        ChatRepository.groups,
        ChatRepository.onlineUsers,
        ChatRepository.settings,
        drafts,
    ) { users, groups, online, settings, edits ->
        build(
            user = users[userId],
            directoryLoaded = users.isNotEmpty(),
            groups = groups.values,
            online = userId in online,
            settings = settings,
            edits = edits,
        )
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS),
        initialValue = ContactProfileUiState(id = userId),
    )

    init {
        viewModelScope.launch {
            ChatRepository.users
                .map { it[userId] }
                .distinctUntilChanged()
                .collect { user ->
                    if (user == null) return@collect
                    // Once an edit has come back from the engine the local shadow
                    // is dropped, so a later change from another device shows up.
                    if (aliasDraft.value?.trim() == user.alias) aliasDraft.value = null
                    if (notesDraft.value == user.notes) notesDraft.value = null
                }
        }
    }

    override fun onCleared() {
        // Leaving the screen inside the debounce window must not lose the note.
        val pending = notesDraft.value
        notesSaveJob?.cancel()
        if (pending == null || pending == lastNotesWritten) return
        val client = EngineHolder.client ?: return
        teardownScope.launch { runCatching { client.setUserNotes(userId, pending) } }
    }

    // --- naming ------------------------------------------------------------

    fun editAlias(text: String) {
        aliasDraft.value = text.take(MAX_ALIAS_LENGTH)
    }

    /**
     * Explicit rather than debounced: an empty alias is a meaningful value - it
     * clears the alias and hands the contact back their own name - so a
     * half-typed field must never be committed on its own.
     */
    fun saveAlias() {
        val wanted = aliasDraft.value?.trim() ?: return
        onEngine { it.aliasUser(userId, wanted) }
    }

    fun editNotes(text: String) {
        notesDraft.value = text.take(MAX_NOTES_LENGTH)
        notesSaveJob?.cancel()
        notesSaveJob = viewModelScope.launch {
            delay(NOTES_DEBOUNCE_MS)
            saveNotes()
        }
    }

    /** Called when the field loses focus, so leaving it never waits out the debounce. */
    fun flushNotes() {
        notesSaveJob?.cancel()
        saveNotes()
    }

    private fun saveNotes() {
        val draft = notesDraft.value ?: return
        if (draft == lastNotesWritten) return
        lastNotesWritten = draft
        onEngine { it.setUserNotes(userId, draft) }
    }

    // --- conversation settings ---------------------------------------------

    fun setRetention(seconds: Long) = onEngine { it.setDMRetention(userId, seconds) }

    fun setMutedUntil(until: Long) = onEngine { it.setDMMutedUntil(userId, until) }

    fun setReadReceipts(choice: ContactPrivacyChoice) = onEngine {
        it.setDMReadReceiptSettings(userId, choice.overrides, choice.enabled)
    }

    fun setTypingIndicators(choice: ContactPrivacyChoice) = onEngine {
        it.setDMTypingIndicatorSettings(userId, choice.overrides, choice.enabled)
    }

    /**
     * The engine has no "delete DM": hiding it with SetOpenDM(false) drops it
     * from the thread list while keeping the contact and their history.
     */
    fun setChatOpen(open: Boolean) = onEngine { it.setOpenDM(userId, open) }

    /**
     * Opens the DM on the way to it. A never-opened or hidden conversation is not
     * in the thread list until the engine has been told, and marking the
     * connection desired is what gets the peer dialled.
     */
    fun openChat() = onEngine {
        it.setOpenDM(userId, true)
        it.userConnectionDesired(userId)
    }

    fun clearHistory() = onEngine { it.clearDMChatHistory(userId) }

    /** Blocking also drops us out of every group we share; the engine reloads their consensus. */
    fun setBlocked(blocked: Boolean) = onEngine {
        if (blocked) it.blockUser(userId) else it.unblockUser(userId)
    }

    // --- internals ---------------------------------------------------------

    private fun build(
        user: User?,
        directoryLoaded: Boolean,
        groups: Collection<Group>,
        online: Boolean,
        settings: Settings,
        edits: Drafts,
    ): ContactProfileUiState {
        // The directory always contains at least our own profile once loaded, so
        // an empty map means the snapshot has not arrived rather than that this
        // contact is gone.
        if (user == null) {
            return ContactProfileUiState(
                id = userId,
                loading = !directoryLoaded,
                missing = directoryLoaded,
            )
        }

        val mutedUntil = user.state.mutedUntil
        return ContactProfileUiState(
            id = userId,
            loading = false,
            missing = false,
            name = user.name,
            alias = user.alias,
            displayName = user.displayName,
            imageIds = user.images,
            online = online,
            blocked = user.blocked,
            chatOpen = user.state.open,
            retention = user.state.retention,
            // A mute is stored as a deadline with a sentinel for "never unmute",
            // so an expired deadline has to read as unmuted.
            muted = mutedUntil == Goengine.MutedForever ||
                mutedUntil > System.currentTimeMillis() / 1000,
            aliasDraft = edits.alias ?: user.alias,
            aliasDirty = edits.alias != null && edits.alias.trim() != user.alias,
            notesDraft = edits.notes ?: user.notes,
            readReceipts = ContactPrivacyChoice.of(
                user.state.overrideReadReceiptSetting,
                user.state.readReceiptsEnabled,
            ),
            typingIndicators = ContactPrivacyChoice.of(
                user.state.overrideTypingIndicatorSetting,
                user.state.typingIndicatorsEnabled,
            ),
            defaultReadReceipts = settings.defaultSendReadReceipts,
            defaultTypingIndicators = settings.defaultSendTypingIndicators,
            groupsInCommon = groups
                .filter { group -> group.users.any { it.id == userId } }
                .map { GroupInCommon(it.id, it.name, it.images) }
                .sortedBy { it.name.lowercase(Locale.getDefault()) },
        )
    }

    /**
     * Engine calls are blocking JNI and any of them can fail (offline, revoked
     * device). A failed profile action must not take the process down, and there
     * is nothing useful to retry: the engine re-emits the authoritative state
     * either way, so the screen falls back to the truth on its own.
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

    private data class Drafts(val alias: String?, val notes: String?)

    companion object {
        fun factory(userId: String): ViewModelProvider.Factory = viewModelFactory {
            initializer { ContactProfileViewModel(userId) }
        }

        private const val TAG = "ContactProfile"
        private const val STOP_TIMEOUT_MS = 5_000L
        private const val NOTES_DEBOUNCE_MS = 800L
        private const val MAX_ALIAS_LENGTH = 128
        private const val MAX_NOTES_LENGTH = 4_096
    }
}
