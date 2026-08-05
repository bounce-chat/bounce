package chat.bounce.ui.threads

import android.util.Log
import androidx.annotation.StringRes
import androidx.compose.runtime.Immutable
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.data.DeliveryState
import chat.bounce.data.InitialSyncState
import chat.bounce.data.ThreadSummary
import chat.bounce.engine.EngineClient
import chat.bounce.engine.EngineHolder
import chat.bounce.engine.User
import chat.bounce.goengine.Goengine
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** One row of the inbox, fully formatted; the screen does no engine-shaped work. */
@Immutable
data class ThreadRow(
    val id: String,
    val name: String,
    val isGroup: Boolean,
    val imageIds: List<String>,
    val preview: String,
    /** Non-null when an unsent draft exists; it replaces [preview]. */
    val draft: String?,
    val time: String,
    val unreadCount: Int,
    /**
     * Delivery glyph for the row, or null when the newest message is not ours.
     * Describes the same message as [preview].
     */
    val outgoingStatus: DeliveryState?,
    val muted: Boolean,
    /** Null for groups, which have no single presence. */
    val online: Boolean?,
    val blocked: Boolean,
    val pendingInvite: Boolean,
    /**
     * Display names of whoever is typing right now. Resolved here rather than
     * formatted, so the row can pick the right localized plural.
     */
    val typingNames: List<String> = emptyList(),
    /** A DM to yourself; the row shows a note badge instead of a presence dot. */
    val isSelf: Boolean = false,
)

@Immutable
data class ContactRow(
    val id: String,
    val name: String,
    val imageIds: List<String>,
)

@Immutable
data class ThreadListState(
    val rows: List<ThreadRow> = emptyList(),
    val query: String = "",
    val online: Boolean = true,
    /** Supersedes [online]: this install has been cut off from the network. */
    val revoked: Boolean = false,
    val syncing: Boolean = false,
    /** Set only during the determinate phase of an initial sync. */
    val syncProgress: Float? = null,
    /** Distinguishes "no chats at all" from "the search matched nothing". */
    val hasAnyThreads: Boolean = false,
)

/** The mute durations the desktop client offers, in the same order. */
enum class MuteDuration(@get:StringRes val label: Int, private val seconds: Long) {
    FiveMinutes(R.string.mute_5_minutes, 5 * 60),
    OneHour(R.string.mute_1_hour, 60 * 60),
    OneDay(R.string.mute_1_day, 24 * 60 * 60),
    OneWeek(R.string.mute_1_week, 7 * 24 * 60 * 60),
    Forever(R.string.mute_forever, 0);

    /** The engine wants an absolute unix timestamp, or its sentinel for "never". */
    fun mutedUntil(): Long =
        if (this == Forever) Goengine.MutedForever else System.currentTimeMillis() / 1000 + seconds
}

class ThreadListViewModel : ViewModel() {

    private val _query = MutableStateFlow("")

    /**
     * Relative labels ("12m", "3h") go stale on their own, so the list is
     * rebuilt on the same 60s cadence the desktop thread rows use.
     */
    private val minuteTick = flow {
        while (true) {
            emit(System.currentTimeMillis())
            delay(60_000L)
        }
    }

    // Two-stage combine: the typed overloads stop at five flows and a row needs
    // six inputs. Drafts and the user directory are separate from the summary
    // because only the row's *rendering* depends on them.
    private val rowInputs: Flow<RowInputs> =
        combine(
            ChatRepository.threads,
            ChatRepository.drafts,
            ChatRepository.users,
            ChatRepository.typing,
        ) { threads, drafts, users, typing -> RowInputs(threads, drafts, users, typing) }

    // Folded into one flow because combine's typed overloads stop at five and
    // the state already uses all five. They belong together anyway: revocation
    // is what connectivity means once it has happened.
    private val connectivity: Flow<Connectivity> =
        combine(ChatRepository.networkOnline, ChatRepository.deviceRevoked) { online, revoked ->
            Connectivity(online = online, revoked = revoked)
        }

    val state: StateFlow<ThreadListState> = combine(
        rowInputs,
        connectivity,
        ChatRepository.initialSync,
        _query,
        minuteTick,
    ) { input, net, sync, query, now ->
        ThreadListState(
            rows = input.threads.filter { it.matches(query) }.map { it.toRow(input, now) },
            query = query,
            online = net.online,
            revoked = net.revoked,
            syncing = sync.isRunning,
            syncProgress = (sync as? InitialSyncState.Progress)?.fraction?.toFloat(),
            hasAnyThreads = input.threads.isNotEmpty(),
        )
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), ThreadListState())

    val contacts: StateFlow<List<ContactRow>> =
        combine(ChatRepository.users, ChatRepository.profile) { users, profile ->
            users.values.asSequence()
                .filter { !it.blocked && it.id != profile?.id }
                .map { ContactRow(it.id, it.displayName, it.images) }
                .sortedBy { it.name.lowercase(Locale.getDefault()) }
                .toList()
        }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    fun setQuery(value: String) {
        _query.value = value
    }

    fun acceptInvite(threadId: String) = onEngine { it.acceptInvite(threadId) }

    fun declineInvite(threadId: String) = onEngine { it.rejectInvite(threadId) }

    /**
     * Opens a hidden or never-opened DM before navigating to it. The engine
     * answers with SetDMState, which is what puts the thread in the list.
     */
    fun openContact(userId: String, onOpened: (String) -> Unit) {
        onEngine {
            it.setOpenDM(userId, true)
            it.userConnectionDesired(userId)
        }
        onOpened(userId)
    }

    fun mute(row: ThreadRow, duration: MuteDuration) = setMutedUntil(row, duration.mutedUntil())

    fun unmute(row: ThreadRow) = setMutedUntil(row, 0L)

    private fun setMutedUntil(row: ThreadRow, until: Long) = onEngine {
        if (row.isGroup) it.setGroupMutedUntil(row.id, until) else it.setDMMutedUntil(row.id, until)
    }

    /** Blocking a group is permanent: it can never invite you again. */
    fun block(row: ThreadRow) = onEngine {
        if (row.isGroup) it.blockGroup(row.id) else it.blockUser(row.id)
    }

    fun unblock(row: ThreadRow) = onEngine { it.unblockUser(row.id) }

    fun clearHistory(row: ThreadRow) = onEngine {
        if (row.isGroup) it.clearGroupChatHistory(row.id) else it.clearDMChatHistory(row.id)
    }

    /**
     * The engine has no "delete DM": hiding it with SetOpenDM(false) drops it
     * from the list while keeping the contact and their history. Leaving a group
     * is modelled as removing yourself from it.
     */
    fun removeThread(row: ThreadRow) {
        val me = ChatRepository.currentUserId
        onEngine {
            when {
                !row.isGroup -> it.setOpenDM(row.id, false)
                me.isNotEmpty() -> it.removeUserFromGroup(row.id, me)
                else -> Log.w(TAG, "cannot leave ${row.id}: no profile loaded")
            }
        }
    }

    /**
     * Engine calls are blocking JNI and any of them can fail (offline, revoked
     * device, insufficient group permissions). A failed inbox action must not
     * take the process down, and there is nothing useful to retry: the engine
     * re-emits the authoritative state either way.
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
        const val TAG = "ThreadList"
    }
}

private data class RowInputs(
    val threads: List<ThreadSummary>,
    val drafts: Map<String, String>,
    val users: Map<String, User>,
    val typing: Map<String, Set<String>>,
)

private data class Connectivity(val online: Boolean, val revoked: Boolean)

private fun ThreadSummary.matches(query: String): Boolean =
    query.isBlank() || name.contains(query.trim(), ignoreCase = true)

private fun ThreadSummary.toRow(input: RowInputs, nowMillis: Long) = ThreadRow(
    id = id,
    name = name,
    isGroup = isGroup,
    imageIds = listOfNotNull(avatarFileId),
    preview = lastMessagePreview,
    // hasDraft is the summary's sort-affecting flag; the text itself lives in
    // the draft map, which also carries drafts typed on another device.
    draft = if (hasDraft) input.drafts[id]?.takeIf { it.isNotBlank() } else null,
    time = relativeTime(nowMillis, lastActivity),
    unreadCount = unreadCount,
    outgoingStatus = outgoingStatus,
    muted = isMuted,
    online = if (isGroup) null else online,
    blocked = !isGroup && input.users[id]?.blocked == true,
    pendingInvite = isPendingInvite,
    typingNames = input.typing[id].orEmpty().mapNotNull { input.users[it]?.displayName },
    isSelf = isSelf,
)

/**
 * The desktop client's thread-row clock (ui/thread_button.go): now, 12m, 3h,
 * Wed, Mar 4, Mar 4 2024.
 */
private fun relativeTime(nowMillis: Long, unixSeconds: Long): String {
    if (unixSeconds <= 0) return NOW

    val elapsed = nowMillis - unixSeconds * 1000
    // A peer's clock can be ahead of ours; a negative age must not render as a
    // huge minute count.
    if (elapsed < MINUTE) return NOW

    val zoned = Instant.ofEpochSecond(unixSeconds).atZone(ZoneId.systemDefault())
    return when {
        elapsed < HOUR -> "${elapsed / MINUTE}m"
        elapsed < DAY -> "${elapsed / HOUR}h"
        elapsed < 7 * DAY -> weekdayFormat.format(zoned)
        elapsed < 365 * DAY -> dayFormat.format(zoned)
        else -> yearFormat.format(zoned)
    }
}

private const val NOW = "now"
private const val MINUTE = 60_000L
private const val HOUR = 60 * MINUTE
private const val DAY = 24 * HOUR

private val weekdayFormat = DateTimeFormatter.ofPattern("EEE", Locale.getDefault())
private val dayFormat = DateTimeFormatter.ofPattern("MMM d", Locale.getDefault())
private val yearFormat = DateTimeFormatter.ofPattern("MMM d yyyy", Locale.getDefault())
