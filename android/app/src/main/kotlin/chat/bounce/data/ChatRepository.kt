package chat.bounce.data

import android.util.Log
import chat.bounce.engine.BulkUpdate
import chat.bounce.engine.DMState
import chat.bounce.engine.Device
import chat.bounce.engine.EngineClient
import chat.bounce.engine.Group
import chat.bounce.engine.GroupActorUpdate
import chat.bounce.engine.InitialState
import chat.bounce.engine.ReadReceipt
import chat.bounce.engine.Settings
import chat.bounce.engine.User
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update

/** Where the engine is in a first-run catch-up from a paired device. */
sealed interface InitialSyncState {
    data object Idle : InitialSyncState
    data object Starting : InitialSyncState
    data class Progress(val fraction: Double) : InitialSyncState
    data object Preparing : InitialSyncState
    data object Complete : InitialSyncState

    /** True while the UI should show a blocking "setting up" state. */
    val isRunning: Boolean
        get() = this is Starting || this is Progress || this is Preparing
}

/**
 * Things the UI must *react* to rather than render: they have no resting state,
 * so they cannot live in a StateFlow without the UI having to remember whether
 * it has already handled them.
 */
sealed interface RepositoryEffect {
    data class ShowError(val message: String) : RepositoryEffect
    data class GroupDeleted(val groupId: String, val actorId: String) : RepositoryEffect
    data class RemovedFromGroup(val groupId: String, val actorId: String) : RepositoryEffect

    /** The invite was rolled back by consensus; the group never really existed. */
    data class GroupRolledBack(val groupId: String) : RepositoryEffect

    data class SyncDeviceRejected(val peer: String) : RepositoryEffect
    data class SyncDeviceAccepted(val userId: String, val references: Boolean) : RepositoryEffect
    data object SyncDeviceAdded : RepositoryEffect
    data class AddUserRejected(val peer: String) : RepositoryEffect
    data class UserAdded(val userId: String) : RepositoryEffect
    data object EncryptedDeviceAdded : RepositoryEffect
    data object EncryptedDeviceRejected : RepositoryEffect

    /**
     * A device key hash stopped matching. The device can still be seen but must
     * not be managed until the user re-verifies it.
     */
    data class EncryptedDeviceManageable(val deviceId: String, val manageable: Boolean) :
        RepositoryEffect

    /** This device's access was revoked from another device; the app is now useless. */
    data object ThisDeviceRevoked : RepositoryEffect

    /** The profile was created on this device; onboarding is finished. */
    data class ProfileCreated(val userId: String) : RepositoryEffect

    /** The engine is shutting down of its own accord. */
    data object EngineQuit : RepositoryEffect
}

/**
 * The single in-memory view of the engine's state.
 *
 * Process-scoped for the same reason [chat.bounce.engine.EngineHolder] is: the
 * foreground service keeps running with no Activity, and the state it
 * accumulates while backgrounded has to still be there when an Activity is
 * recreated. A ViewModel-scoped repository would throw away every message
 * received while the UI was gone, then need a full reload to get it back.
 *
 * THREADING - all mutators are safe to call from any thread. Writes go through
 * MutableStateFlow.update, which is an atomic compare-and-set, so a mutator's
 * lambda may run more than once and must stay free of side effects. In practice
 * [EngineEventPump] is the only writer for engine-driven state, so contention is
 * limited to optimistic UI updates racing the pump.
 */
object ChatRepository {

    // Owns the derived `threads` flow. Never cancelled: the object outlives every
    // Activity and Service, and a cancelled scope would silently stop updating.
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    private val _profile = MutableStateFlow<User?>(null)
    val profile: StateFlow<User?> = _profile.asStateFlow()

    private val _initialStateLoaded = MutableStateFlow(false)

    /**
     * True once [applyInitialState] has installed a snapshot from the engine.
     *
     * The startup gate needs this and cannot infer it from anything else. A null
     * [profile] is ambiguous on its own - it means either "this is a first run"
     * or "the snapshot has not arrived yet" - and EngineHolder.client goes
     * non-null the instant the engine object exists, which is *before*
     * GetInitialState is even called, let alone returned. Guessing across that
     * window shows the first-run flow to an existing user.
     */
    val initialStateLoaded: StateFlow<Boolean> = _initialStateLoaded.asStateFlow()

    private val _settings = MutableStateFlow(Settings())
    val settings: StateFlow<Settings> = _settings.asStateFlow()

    private val _users = MutableStateFlow<Map<String, User>>(emptyMap())
    val users: StateFlow<Map<String, User>> = _users.asStateFlow()

    private val _groups = MutableStateFlow<Map<String, Group>>(emptyMap())
    val groups: StateFlow<Map<String, Group>> = _groups.asStateFlow()

    private val _devices = MutableStateFlow<List<Device>>(emptyList())
    val devices: StateFlow<List<Device>> = _devices.asStateFlow()

    /** Thread ID to items, oldest first. */
    private val _messagesByThread = MutableStateFlow<Map<String, List<ConversationItem>>>(emptyMap())
    val messagesByThread: StateFlow<Map<String, List<ConversationItem>>> =
        _messagesByThread.asStateFlow()

    private val _drafts = MutableStateFlow<Map<String, String>>(emptyMap())
    val drafts: StateFlow<Map<String, String>> = _drafts.asStateFlow()

    /**
     * When each thread was last opened, thread ID to unix seconds.
     *
     * Held here rather than read off the User/Group DTOs because
     * SetDMLastOpened and SetGroupLastOpened write the engine's database and
     * emit nothing, so the DTOs only carry a value that is correct as of the
     * last snapshot. Seeded from that snapshot, then maintained by whoever opens
     * a thread.
     */
    private val _lastOpened = MutableStateFlow<Map<String, Long>>(emptyMap())
    val lastOpened: StateFlow<Map<String, Long>> = _lastOpened.asStateFlow()

    fun setLastOpened(threadId: String, timestamp: Long) {
        _lastOpened.update { it + (threadId to timestamp) }
    }

    /** Thread ID to the users currently typing in it. Threads with nobody typing are absent. */
    private val _typing = MutableStateFlow<Map<String, Set<String>>>(emptyMap())
    val typing: StateFlow<Map<String, Set<String>>> = _typing.asStateFlow()

    private val _onlineUsers = MutableStateFlow<Set<String>>(emptySet())
    val onlineUsers: StateFlow<Set<String>> = _onlineUsers.asStateFlow()

    private val _networkOnline = MutableStateFlow(false)
    val networkOnline: StateFlow<Boolean> = _networkOnline.asStateFlow()

    /**
     * This install has been revoked by another of the user's devices.
     *
     * Terminal: the engine refuses to start the network while the revoked row is
     * in its database, so this never goes back to false within a session and is
     * deliberately left out of [clear] - dropping it during an engine restart
     * would show "offline" for the reconnect that is never going to happen.
     */
    private val _deviceRevoked = MutableStateFlow(false)
    val deviceRevoked: StateFlow<Boolean> = _deviceRevoked.asStateFlow()

    /**
     * A pending request for the inbox to jump back to the top.
     *
     * Latched rather than sent as an effect because the thread list is not
     * composed at the moment it is raised - sending a message means the
     * conversation is on screen - so there is nobody listening yet. The list
     * reads it when it next appears and calls [threadListScrolledToTop].
     */
    private val _scrollThreadListToTop = MutableStateFlow(false)
    val scrollThreadListToTop: StateFlow<Boolean> = _scrollThreadListToTop.asStateFlow()

    /**
     * Sending moves a thread to the top of the list, so the inbox should be
     * showing the top when the user returns to it rather than wherever they had
     * scrolled to before opening the conversation.
     */
    fun requestThreadListScrollToTop() {
        _scrollThreadListToTop.value = true
    }

    fun threadListScrolledToTop() {
        _scrollThreadListToTop.value = false
    }

    /**
     * Another of our own devices is currently in the foreground. The engine
     * already uses this to suppress notifications; the UI uses it to explain why
     * this device is quiet.
     */
    private val _anotherDeviceActive = MutableStateFlow(false)
    val anotherDeviceActive: StateFlow<Boolean> = _anotherDeviceActive.asStateFlow()

    private val _initialSync = MutableStateFlow<InitialSyncState>(InitialSyncState.Idle)
    val initialSync: StateFlow<InitialSyncState> = _initialSync.asStateFlow()

    /** File UUID to download fraction in 0..1. */
    private val _fileProgress = MutableStateFlow<Map<String, Double>>(emptyMap())
    val fileProgress: StateFlow<Map<String, Double>> = _fileProgress.asStateFlow()

    /**
     * A message-ID index over [messagesByThread].
     *
     * Notifications look a message up by ID on every post, and MessageDelivered
     * fires once per (message, recipient) - both would otherwise scan every
     * thread. Kept private because it is a lookup accelerator, not state.
     */
    private val messageIndex = MutableStateFlow<Map<String, ConversationItem.Message>>(emptyMap())

    // DROP_OLDEST rather than suspend: an effect emitted from the pump must never
    // block the engine's event stream because no Activity is currently collecting.
    private val _effects = MutableSharedFlow<RepositoryEffect>(
        replay = 0,
        extraBufferCapacity = 64,
        onBufferOverflow = BufferOverflow.DROP_OLDEST,
    )
    val effects: SharedFlow<RepositoryEffect> = _effects.asSharedFlow()

    /** Empty until a profile exists. Read directly rather than collected; it changes once. */
    val currentUserId: String get() = _profile.value?.id.orEmpty()

    private data class ThreadInputs(
        val users: Map<String, User>,
        val groups: Map<String, Group>,
        val items: Map<String, List<ConversationItem>>,
    )

    // Two-stage combine: the typed overloads stop at five flows and the derivation
    // needs six.
    private val threadInputs: Flow<ThreadInputs> =
        combine(users, groups, messagesByThread) { u, g, items -> ThreadInputs(u, g, items) }

    /**
     * Every visible conversation, newest activity first.
     *
     * Eagerly started: the notification layer and the service read
     * `threads.value` synchronously when there is no UI collecting.
     */
    val threads: StateFlow<List<ThreadSummary>> =
        combine(threadInputs, drafts, onlineUsers, profile, lastOpened) {
                input, draftText, online, me, opened ->
            buildThreads(input, draftText, online, me, opened)
        }.stateIn(scope, SharingStarted.Eagerly, emptyList())

    // --- lookups ---------------------------------------------------------------

    fun messageById(id: String): ConversationItem.Message? = messageIndex.value[id]

    fun threadFor(threadId: String): ThreadSummary? = threads.value.firstOrNull { it.id == threadId }

    fun itemsIn(threadId: String): List<ConversationItem> =
        _messagesByThread.value[threadId].orEmpty()

    fun userById(id: String): User? = _users.value[id]

    fun groupById(id: String): Group? = _groups.value[id]

    /** True when the thread exists and is a group rather than a DM. */
    fun isGroup(threadId: String): Boolean = _groups.value.containsKey(threadId)

    // --- bulk load -------------------------------------------------------------

    /**
     * Replaces everything with a fresh snapshot from the engine.
     *
     * BLOCKING in Go; [EngineClient.initialState] already moves it to
     * Dispatchers.IO.
     */
    suspend fun load(client: EngineClient) {
        applyInitialState(client.initialState())
    }

    /** Full replace. [load] is the normal entry point; this is split out for tests. */
    fun applyInitialState(state: InitialState) {
        val myId = state.profile?.id.orEmpty()

        _profile.value = state.profile
        _settings.value = state.settings
        _networkOnline.value = state.networkOnline
        _devices.value = state.syncDevices
        _users.value = state.users.associateBy { it.id }
        _groups.value = state.groups.associateBy { it.id }
        _drafts.value = state.drafts.associate { it.thread to it.text }
        _lastOpened.value = buildMap {
            state.users.forEach { put(it.id, it.state.lastOpened) }
            state.groups.forEach { put(it.id, it.lastOpened) }
            state.profile?.let { put(it.id, it.state.lastOpened) }
        }
        _fileProgress.value = state.fileProgress.associate { it.id to it.progress }
        // Presence and typing are deliberately left alone: both are edge-triggered
        // by the engine and absent from the snapshot, so resetting them here would
        // mark every contact offline until their next transition.

        val items = conversationItemsFrom(state, myId)
        _messagesByThread.value = items.groupBy { it.threadId }
            .mapValues { (_, list) -> list.sortedBy { it.timestamp } }
        messageIndex.value = items.filterIsInstance<ConversationItem.Message>().associateBy { it.id }

        if (state.deviceRevoked) markDeviceRevoked()

        // Last, deliberately. Every assignment above is a separate StateFlow
        // write and composition runs on another thread, so a collector can
        // observe this function half-applied - profile set but messages not yet.
        // Flipping this only at the end gives the gate a single edge to wait on
        // that means "all of it is installed".
        _initialStateLoaded.value = true
    }

    /**
     * Merges a scoped snapshot - what [EngineClient.dmHistory] returns - into the
     * existing item lists without touching users, groups or settings.
     */
    fun applyHistory(state: InitialState) {
        addItems(conversationItemsFrom(state, currentUserId))
    }

    /** Applies the aggregate the engine sends after replaying a catch-up. */
    fun applyBulkUpdate(update: BulkUpdate) {
        val myId = currentUserId
        val items = buildList {
            update.directMessages.values.forEach { msgs ->
                msgs?.forEach { add(it.toConversationItem(myId)) }
            }
            update.groupMessages.values.forEach { msgs ->
                msgs?.forEach { add(it.toConversationItem(myId)) }
            }
        }
        // Counts, because a batch that silently loses messages is invisible
        // otherwise: the engine only announces a message once per catch-up, and
        // anything dropped here never reappears until the next one.
        val before = _messagesByThread.value.values.sumOf { it.size }
        addItems(items)
        val after = _messagesByThread.value.values.sumOf { it.size }
        Log.i(
            TAG,
            "bulk update: ${update.directMessages.values.sumOf { it?.size ?: 0 }} dm + " +
                "${update.groupMessages.values.sumOf { it?.size ?: 0 }} gm -> ${items.size} items, " +
                "held ${before} -> ${after} (+${after - before} new, ${items.size - (after - before)} already present)",
        )

        // Receipts and seen-markers are applied in ONE pass, not one at a time.
        // Each individual mutateMessage copies the entire message index and
        // dirties _messagesByThread, which forces `threads` - eagerly started,
        // and rescanning every item of every thread - to recompute. A catch-up
        // carrying S markers therefore used to cost O(S x total messages),
        // which is why a device with a lot of history appeared to stall and
        // then complete all at once.
        val receiptsByTarget = update.readReceipts.values.filterNotNull().flatten().groupBy { it.target }
        val seen = update.seen.toHashSet()
        val touched = receiptsByTarget.keys + seen
        if (touched.isEmpty()) return

        editMessages(touched) { message ->
            var next = message
            receiptsByTarget[message.id]?.forEach { receipt ->
                if (next.readReceipts.none { it.id == receipt.id }) {
                    next = next.copy(readReceipts = next.readReceipts + receipt)
                }
            }
            if (message.id in seen && !next.seen) next = next.copy(seen = true)
            next
        }
    }

    /**
     * Applies one transform to many messages with a single state update.
     *
     * [mutateMessage] is the right shape for a single event but the wrong shape
     * for a batch: its per-call cost is proportional to the total number of
     * messages held, so calling it in a loop is quadratic.
     */
    private fun editMessages(
        messageIds: Set<String>,
        transform: (ConversationItem.Message) -> ConversationItem.Message,
    ) {
        val index = messageIndex.value
        val threadIds = messageIds.mapNotNullTo(HashSet()) { index[it]?.threadId }
        if (threadIds.isEmpty()) return

        val edited = HashMap<String, ConversationItem.Message>()
        _messagesByThread.update { current ->
            val next = current.toMutableMap()
            for (threadId in threadIds) {
                val list = current[threadId] ?: continue
                var changed = false
                val replacement = list.map { item ->
                    if (item is ConversationItem.Message && item.id in messageIds) {
                        val updated = transform(item)
                        if (updated != item) {
                            changed = true
                            edited[updated.id] = updated
                        }
                        updated
                    } else {
                        item
                    }
                }
                if (changed) next[threadId] = replacement
            }
            next
        }
        if (edited.isNotEmpty()) messageIndex.update { it + edited }
    }

    /** Drops everything. Call when the engine stops so a restart cannot show stale state. */
    fun clear() {
        _profile.value = null
        _settings.value = Settings()
        _users.value = emptyMap()
        _groups.value = emptyMap()
        _devices.value = emptyList()
        _messagesByThread.value = emptyMap()
        messageIndex.value = emptyMap()
        _drafts.value = emptyMap()
        _lastOpened.value = emptyMap()
        _typing.value = emptyMap()
        _onlineUsers.value = emptySet()
        _networkOnline.value = false
        _anotherDeviceActive.value = false
        _scrollThreadListToTop.value = false
        _initialSync.value = InitialSyncState.Idle
        _fileProgress.value = emptyMap()
    }

    // --- effects ---------------------------------------------------------------

    /** Non-suspending so the pump and JNI-adjacent code can never be blocked by it. */
    fun emit(effect: RepositoryEffect) {
        _effects.tryEmit(effect)
    }

    // --- profile, settings, users, groups, devices -----------------------------

    fun setProfile(user: User) {
        _profile.value = user
        upsertUser(user)
    }

    fun setSettings(settings: Settings) {
        _settings.value = settings
    }

    fun upsertUser(user: User) {
        _users.update { it + (user.id to user) }
        if (user.id == _profile.value?.id) _profile.value = user
    }

    /**
     * Records a user learned in passing - a group invitee we have no DM with -
     * without clobbering the richer record of an existing contact.
     */
    fun addUserIfAbsent(user: User) {
        if (user.id.isEmpty()) return
        _users.update { if (it.containsKey(user.id)) it else it + (user.id to user) }
    }

    fun setDMState(userId: String, state: DMState) {
        _users.update { current ->
            val existing = current[userId] ?: return@update current
            current + (userId to existing.copy(state = state))
        }
    }

    fun upsertGroup(group: Group) {
        _groups.update { it + (group.id to group) }
        // A group snapshot is the only place some members' names ever arrive.
        group.users.forEach(::addUserIfAbsent)
        group.invites.forEach(::addUserIfAbsent)
    }

    fun removeGroup(groupId: String) {
        _groups.update { it - groupId }
        _drafts.update { it - groupId }
        _typing.update { it - groupId }
        val removed = _messagesByThread.value[groupId].orEmpty()
        _messagesByThread.update { it - groupId }
        if (removed.isNotEmpty()) {
            val ids = removed.mapTo(HashSet()) { it.id }
            messageIndex.update { index -> index.filterKeys { it !in ids } }
        }
    }

    fun upsertDevice(device: Device) {
        _devices.update { current ->
            val index = current.indexOfFirst { it.id == device.id }
            if (index < 0) current + device else current.toMutableList().also { it[index] = device }
        }
    }

    fun removeDevice(deviceId: String) {
        _devices.update { current -> current.filterNot { it.id == deviceId } }
    }

    fun renameDevice(deviceId: String, name: String) {
        updateDevice(deviceId) { it.copy(name = name) }
    }

    fun setDeviceLastSeen(deviceId: String, lastSeen: Long) {
        updateDevice(deviceId) { it.copy(lastSeen = lastSeen) }
    }

    fun setDeviceOnline(deviceId: String, online: Boolean) {
        updateDevice(deviceId) { it.copy(online = online) }
    }

    private fun updateDevice(deviceId: String, transform: (Device) -> Device) {
        _devices.update { current ->
            val index = current.indexOfFirst { it.id == deviceId }
            if (index < 0) current
            else current.toMutableList().also { it[index] = transform(it[index]) }
        }
    }

    fun setUserOnline(userId: String, online: Boolean) {
        _onlineUsers.update { if (online) it + userId else it - userId }
    }

    /**
     * Records the revocation and forces the network flag down with it.
     *
     * The engine has already torn Tor down by the time this is called, but it
     * reports that by simply never emitting NetworkOnline again rather than by
     * emitting an offline event, so without this the last value observed would
     * stay true and the banner would never appear.
     */
    fun markDeviceRevoked() {
        _deviceRevoked.value = true
        _networkOnline.value = false
        emit(RepositoryEffect.ThisDeviceRevoked)
    }

    fun setNetworkOnline(online: Boolean) {
        // A revoked device is never coming back online; ignoring a late event
        // keeps a race between the revocation and an in-flight status update
        // from clearing the banner.
        if (_deviceRevoked.value) return
        _networkOnline.value = online
    }

    fun setAnotherDeviceActive(active: Boolean) {
        _anotherDeviceActive.value = active
    }

    fun setInitialSync(state: InitialSyncState) {
        _initialSync.value = state
    }

    // --- drafts, typing, files -------------------------------------------------

    fun setDraft(threadId: String, text: String) {
        _drafts.update { if (text.isEmpty()) it - threadId else it + (threadId to text) }
    }

    fun setTyping(userId: String, threadId: String, typing: Boolean) {
        _typing.update { current ->
            val existing = current[threadId].orEmpty()
            val next = if (typing) existing + userId else existing - userId
            when {
                next == existing -> current
                next.isEmpty() -> current - threadId
                else -> current + (threadId to next)
            }
        }
    }

    fun setFileProgress(fileId: String, fraction: Double) {
        _fileProgress.update { it + (fileId to fraction) }
    }

    /**
     * The blob is now complete on disk. Nothing is evicted from [ImageCache]:
     * it only ever caches successful decodes, so a file that was missing was
     * never cached in the first place.
     */
    fun fileCompleted(fileId: String) {
        setFileProgress(fileId, 1.0)
    }

    // --- conversation items ----------------------------------------------------

    fun addItem(item: ConversationItem) {
        _messagesByThread.update { current ->
            current + (item.threadId to insertSorted(current[item.threadId].orEmpty(), item))
        }
        if (item is ConversationItem.Message) {
            messageIndex.update { it + (item.id to item) }
        }
    }

    /**
     * Bulk insert. Rebuilds each affected thread once instead of per item, which
     * matters on a first sync where this arrives with the whole account history.
     */
    fun addItems(items: Collection<ConversationItem>) {
        if (items.isEmpty()) return
        val byThread = items.groupBy { it.threadId }
        _messagesByThread.update { current ->
            val merged = current.toMutableMap()
            for ((threadId, incoming) in byThread) {
                val deduped = LinkedHashMap<String, ConversationItem>()
                merged[threadId].orEmpty().forEach { deduped[it.id] = it }
                incoming.forEach { deduped[it.id] = it }
                // sortedBy is stable, so same-second items keep arrival order.
                merged[threadId] = deduped.values.sortedBy { it.timestamp }
            }
            merged
        }
        val messages = items.filterIsInstance<ConversationItem.Message>()
        if (messages.isNotEmpty()) {
            messageIndex.update { index -> index + messages.associateBy { it.id } }
        }
    }

    /** Removes an item from whichever thread holds it. IDs are globally unique. */
    fun deleteItem(itemId: String) {
        _messagesByThread.update { current ->
            var changed = false
            val next = current.mapValues { (_, list) ->
                val filtered = list.filterNot { it.id == itemId }
                if (filtered.size != list.size) changed = true
                filtered
            }
            if (changed) next else current
        }
        messageIndex.update { if (it.containsKey(itemId)) it - itemId else it }
    }

    /**
     * Drops everything a clear-history frame covers. The frame carries the cut-off
     * rather than a list of IDs, and the per-item DeleteItem callbacks that
     * accompany it are not guaranteed for every thread type.
     */
    fun clearThreadBefore(threadId: String, clearTime: Long) {
        val removed = _messagesByThread.value[threadId].orEmpty().filter { it.timestamp <= clearTime }
        if (removed.isEmpty()) return
        _messagesByThread.update { current ->
            val list = current[threadId] ?: return@update current
            current + (threadId to list.filter { it.timestamp > clearTime })
        }
        val ids = removed.mapTo(HashSet()) { it.id }
        messageIndex.update { index -> index.filterKeys { it !in ids } }
    }

    fun markSeen(messageId: String) {
        mutateMessage(messageId) { if (it.seen) it else it.copy(seen = true) }
    }

    fun markUndeliverable(messageId: String) {
        mutateMessage(messageId) { it.copy(undeliverable = true) }
    }

    fun markDelivered(messageId: String, userId: String) {
        mutateMessage(messageId) {
            if (userId in it.deliveredTo) it else it.copy(deliveredTo = it.deliveredTo + userId)
        }
    }

    fun addReadReceipt(receipt: ReadReceipt) {
        mutateMessage(receipt.target) { message ->
            if (message.readReceipts.any { it.id == receipt.id }) message
            else message.copy(readReceipts = message.readReceipts + receipt)
        }
    }

    /** Local-only: the engine is told separately via markAllDirectMessagesAsRead. */
    fun markThreadRead(threadId: String) {
        val unread = _messagesByThread.value[threadId].orEmpty()
            .filterIsInstance<ConversationItem.Message>()
            .filter { !it.seen && !it.outgoing }
        if (unread.isEmpty()) return
        val unreadIds = unread.mapTo(HashSet()) { it.id }
        _messagesByThread.update { current ->
            val list = current[threadId] ?: return@update current
            current + (threadId to list.map {
                if (it is ConversationItem.Message && it.id in unreadIds) it.copy(seen = true) else it
            })
        }
        messageIndex.update { index ->
            index + unread.associate { it.id to it.copy(seen = true) }
        }
    }

    private fun mutateMessage(
        messageId: String,
        transform: (ConversationItem.Message) -> ConversationItem.Message,
    ) {
        val threadId = messageIndex.value[messageId]?.threadId ?: return
        var updated: ConversationItem.Message? = null
        _messagesByThread.update { current ->
            val list = current[threadId] ?: return@update current
            val index = list.indexOfFirst { it is ConversationItem.Message && it.id == messageId }
            if (index < 0) return@update current
            val next = transform(list[index] as ConversationItem.Message)
            updated = next
            current + (threadId to list.toMutableList().also { it[index] = next })
        }
        updated?.let { message -> messageIndex.update { it + (messageId to message) } }
    }

    /**
     * Items are held oldest-first. New arrivals are almost always the newest, so
     * the common case is an append; the scan only runs for out-of-order catch-up
     * frames.
     */
    private fun insertSorted(
        list: List<ConversationItem>,
        item: ConversationItem,
    ): List<ConversationItem> {
        val existing = list.indexOfFirst { it.id == item.id }
        if (existing >= 0) {
            // An item's timestamp is immutable, so replacing in place keeps the order.
            return list.toMutableList().also { it[existing] = item }
        }
        if (list.isEmpty() || list.last().timestamp <= item.timestamp) return list + item
        val insertAt = list.indexOfFirst { it.timestamp > item.timestamp }
        return list.toMutableList().also { it.add(if (insertAt < 0) list.size else insertAt, item) }
    }

    // --- derivation ------------------------------------------------------------

    private fun buildThreads(
        input: ThreadInputs,
        draftText: Map<String, String>,
        online: Set<String>,
        me: User?,
        opened: Map<String, Long>,
    ): List<ThreadSummary> {
        val myId = me?.id.orEmpty()
        // The row's timestamp is the newest thing that actually happened in the
        // conversation, and the engine's LastActivity is only a fallback for a
        // thread with no items yet. LastActivity is a peering heuristic, not a
        // conversation clock: user.BeforeCreate sets it to now() when the row is
        // created, so on a device that has just paired every contact was created
        // at sync time and every thread would claim activity "now", burying the
        // real ordering. chat/device_pool.go uses it for who-to-dial, which is
        // what it is actually for.
        val out = ArrayList<ThreadSummary>(input.groups.size + input.users.size)

        for (group in input.groups.values) {
            val items = input.items[group.id].orEmpty()
            out += ThreadSummary(
                id = group.id,
                name = group.name,
                isGroup = true,
                avatarFileId = group.images.lastOrNull(),
                lastActivity = items.lastOrNull()?.timestamp ?: group.lastActivity,
                lastMessagePreview = previewOf(items),
                lastSystemEvent = lastSystemEventOf(items),
                unreadCount = unreadCount(items),
                outgoingStatus = outgoingStatusOf(items, myId),
                mutedUntil = group.mutedUntil,
                isPendingInvite = group.isPendingInvite,
                hasDraft = draftText[group.id]?.isNotBlank() == true,
                lastOpened = opened[group.id] ?: 0L,
                online = group.users.any { it.id != myId && it.id in online },
            )
        }

        for (user in input.users.values) {
            // You can be in the users map even though InitialState.Users excludes
            // the profile: Group.Users contains every member including yourself,
            // and applying a group adds them all. The note-to-self thread is
            // built from the profile below, so without this the same thread id is
            // emitted twice - and a duplicate key is a hard crash in LazyColumn,
            // not a rendering glitch.
            if (user.id == myId) continue

            val items = input.items[user.id].orEmpty()
            // A contact only becomes a thread once the DM is opened or something
            // has been said; otherwise the list would show the whole address book.
            if (!user.state.open && items.isEmpty()) continue
            out += ThreadSummary(
                id = user.id,
                name = user.displayName,
                isGroup = false,
                avatarFileId = user.images.lastOrNull(),
                lastActivity = items.lastOrNull()?.timestamp ?: user.state.lastActivity,
                lastMessagePreview = previewOf(items),
                lastSystemEvent = lastSystemEventOf(items),
                unreadCount = unreadCount(items),
                outgoingStatus = outgoingStatusOf(items, myId),
                mutedUntil = user.state.mutedUntil,
                isPendingInvite = false,
                hasDraft = draftText[user.id]?.isNotBlank() == true,
                lastOpened = opened[user.id] ?: 0L,
                online = user.id in online,
            )
        }

        // A DM to yourself is an ordinary thread to the engine - chat/direct_message.go
        // scopes it to sync devices and getDestination() resolves xor(Nil, myID)
        // back to your own user id - but it is the one thread that cannot be
        // built from the users map, because InitialState.Users is queried with
        // `profile = false` and so never contains you. Without this the messages
        // exist in the repository with nowhere to appear.
        if (me != null) {
            val items = input.items[me.id].orEmpty()
            if (items.isNotEmpty() || me.state.open) {
                out += ThreadSummary(
                    id = me.id,
                    name = me.displayName,
                    isGroup = false,
                    avatarFileId = me.images.lastOrNull(),
                    lastActivity = items.lastOrNull()?.timestamp ?: me.state.lastActivity,
                    lastMessagePreview = previewOf(items),
                    lastSystemEvent = lastSystemEventOf(items),
                    // Always zero: every message here is your own, and
                    // unreadCount only counts incoming ones. Called anyway so
                    // the definition of unread stays in one place.
                    unreadCount = unreadCount(items),
                    // Every message here is ours, so this tops out at
                    // SyncedToMyDevices - which is exactly the useful signal for
                    // a note to self: whether the other devices have it yet.
                    outgoingStatus = outgoingStatusOf(items, myId),
                    mutedUntil = me.state.mutedUntil,
                    isPendingInvite = false,
                    hasDraft = draftText[me.id]?.isNotBlank() == true,
                    lastOpened = opened[me.id] ?: 0L,
                    // Presence is meaningless for yourself; the row shows a
                    // note-to-self badge instead of an online dot.
                    online = false,
                    isSelf = true,
                )
            }
        }

        out.sortByDescending { it.sortKey }

        // Thread ids arrive from three independent sources - groups, contacts,
        // and the profile - so a collision is possible in a way a single source
        // would not be. LazyColumn treats a repeated key as fatal, which turns a
        // data bug into a process kill, so drop duplicates rather than crash.
        // Loudly: this is a bug wherever it fires, not something to absorb
        // quietly.
        val deduped = out.distinctBy { it.id }
        if (deduped.size != out.size) {
            val repeated = out.groupingBy { it.id }.eachCount().filterValues { it > 1 }.keys
            Log.w(TAG, "dropped duplicate thread rows for $repeated")
        }
        return deduped
    }

    /**
     * Delivery state of the newest message when it is ours, else null.
     *
     * Picks the same message [previewOf] does - newest Message, skipping system
     * events - so the row's glyph and its preview text always describe one and
     * the same message.
     */
    private fun outgoingStatusOf(items: List<ConversationItem>, myId: String): DeliveryState? {
        val message = items.lastOrNull() as? ConversationItem.Message ?: return null
        if (!message.outgoing) return null
        return deliveryStateOf(message, myId)
    }

    /**
     * The newest item, when it is a system event rather than a message.
     *
     * The row cannot build this sentence itself - it needs the user directory and
     * localized strings - so the event is handed over whole and turned into text
     * by the same code the conversation history uses.
     */
    private fun lastSystemEventOf(items: List<ConversationItem>): ConversationItem.SystemEvent? =
        items.lastOrNull() as? ConversationItem.SystemEvent

    /**
     * Preview text for the newest item, empty when that item is a system event.
     *
     * Keyed off the last *item* rather than the last message on purpose: a group
     * whose newest event is "Ada was invited" should say so, not quote a message
     * from before it. That matches the desktop client, where thread_item.go calls
     * setLastAction for a status change and it replaces the row's message line.
     */
    private fun previewOf(items: List<ConversationItem>): String {
        val message = items.lastOrNull() as? ConversationItem.Message ?: return ""
        if (message.text.isNotBlank()) return message.text
        return message.imageAttachments.firstOrNull()?.name
            ?: message.fileAttachments.firstOrNull()?.name
            ?: ""
    }

    /** System events never count, matching the desktop client's countsAsUnread. */
    private fun unreadCount(items: List<ConversationItem>): Int =
        items.count { it is ConversationItem.Message && !it.seen && !it.outgoing }

    // --- InitialState to items -------------------------------------------------

    private fun conversationItemsFrom(state: InitialState, myId: String): List<ConversationItem> {
        val items = ArrayList<ConversationItem>()

        state.directMessages.mapTo(items) { it.toConversationItem(myId) }
        state.groupMessages.mapTo(items) { it.toConversationItem(myId) }

        state.updateDMRetentions.mapTo(items) {
            ConversationItem.SystemEvent(
                id = it.id,
                threadId = it.thread,
                timestamp = it.timestamp,
                kind = SystemEventKind.RETENTION_CHANGED,
                actorId = it.actor,
                retention = it.retention,
                seen = it.seen,
            )
        }
        state.updateGroupRetentions.mapTo(items) {
            ConversationItem.SystemEvent(
                id = it.id,
                threadId = it.thread,
                timestamp = it.timestamp,
                kind = SystemEventKind.RETENTION_CHANGED,
                actorId = it.actor,
                retention = it.retention,
                seen = it.seen,
            )
        }
        // The engine timestamps a clear-history frame with when it was *sent*, but
        // the row belongs where the deletion cut, so ClearTime is the sort key.
        state.updateDMClearHistories.mapTo(items) {
            ConversationItem.SystemEvent(
                id = it.id,
                threadId = it.thread,
                timestamp = it.clearTime,
                kind = SystemEventKind.HISTORY_CLEARED,
                actorId = it.actor,
                seen = it.seen,
            )
        }
        state.updateGroupClearHistories.mapTo(items) {
            ConversationItem.SystemEvent(
                id = it.id,
                threadId = it.thread,
                timestamp = it.clearTime,
                kind = SystemEventKind.HISTORY_CLEARED,
                actorId = it.actor,
                seen = it.seen,
            )
        }
        state.updateGroupNames.mapTo(items) {
            ConversationItem.SystemEvent(
                id = it.id,
                threadId = it.thread,
                timestamp = it.timestamp,
                kind = SystemEventKind.GROUP_RENAMED,
                actorId = it.actor,
                name = it.name,
                seen = it.seen,
            )
        }
        state.updateGroupInvitedUsers.mapTo(items) {
            ConversationItem.SystemEvent(
                id = it.id,
                threadId = it.thread,
                timestamp = it.timestamp,
                kind = SystemEventKind.USER_INVITED,
                actorId = it.actor,
                targetUserId = it.user.id,
                name = it.user.displayName,
                seen = it.seen,
            )
        }
        state.updateGroupRemoveUsers.mapTo(items) {
            ConversationItem.SystemEvent(
                id = it.id,
                threadId = it.thread,
                timestamp = it.timestamp,
                kind = SystemEventKind.USER_REMOVED,
                actorId = it.actor,
                targetUserId = it.user,
                seen = it.seen,
            )
        }

        state.updateGroupAdminPromotions.mapTo(items) {
            it.toSystemEvent(SystemEventKind.ADMIN_PROMOTED)
        }
        state.updateGroupAdminDemotions.mapTo(items) {
            it.toSystemEvent(SystemEventKind.ADMIN_DEMOTED)
        }
        state.userManagementRestricted.mapTo(items) {
            it.toSystemEvent(SystemEventKind.USER_MANAGEMENT_RESTRICTED)
        }
        state.userManagementUnrestricted.mapTo(items) {
            it.toSystemEvent(SystemEventKind.USER_MANAGEMENT_UNRESTRICTED)
        }
        state.groupEditsRestricted.mapTo(items) {
            it.toSystemEvent(SystemEventKind.GROUP_EDITS_RESTRICTED)
        }
        state.groupEditsUnrestricted.mapTo(items) {
            it.toSystemEvent(SystemEventKind.GROUP_EDITS_UNRESTRICTED)
        }
        state.postingRestricted.mapTo(items) {
            it.toSystemEvent(SystemEventKind.POSTING_RESTRICTED)
        }
        state.postingUnrestricted.mapTo(items) {
            it.toSystemEvent(SystemEventKind.POSTING_UNRESTRICTED)
        }
        state.userBlockedGroups.mapTo(items) {
            it.toSystemEvent(SystemEventKind.USER_BLOCKED_GROUP)
        }
        state.userChangedGroupImages.mapTo(items) {
            it.toSystemEvent(SystemEventKind.GROUP_IMAGE_CHANGED)
        }
        state.revokedInvites.mapTo(items) {
            it.toSystemEvent(SystemEventKind.INVITE_REVOKED)
        }
        state.acceptedInvites.mapTo(items) {
            it.toSystemEvent(SystemEventKind.INVITE_ACCEPTED)
        }
        state.rejectedInvites.mapTo(items) {
            it.toSystemEvent(SystemEventKind.INVITE_REJECTED)
        }

        // Profile changes are not scoped to a thread on the wire; they belong in
        // every conversation the user appears in, which is how the desktop client
        // fans them out too.
        val threadScope = ThreadScope(
            openDmThreadIds = state.users.filter { it.state.open }.mapTo(HashSet()) { it.id },
            groups = state.groups,
            myId = myId,
        )
        state.updateUserNames.forEach { update ->
            threadScope.threadsFor(update.user).mapTo(items) { threadId ->
                ConversationItem.SystemEvent(
                    id = update.id,
                    threadId = threadId,
                    timestamp = update.timestamp,
                    kind = SystemEventKind.USER_RENAMED,
                    actorId = update.user,
                    targetUserId = update.user,
                    name = update.name,
                    previousName = update.oldName,
                )
            }
        }
        state.updateUserImages.forEach { update ->
            threadScope.threadsFor(update.user).mapTo(items) { threadId ->
                ConversationItem.SystemEvent(
                    id = update.id,
                    threadId = threadId,
                    timestamp = update.timestamp,
                    kind = SystemEventKind.USER_IMAGE_CHANGED,
                    actorId = update.user,
                    targetUserId = update.user,
                )
            }
        }

        return items
    }

    /**
     * Which threads a profile-level change about [userId] should appear in.
     *
     * Split out because live UserNameUpdated / UserImageUpdated / UserAliased
     * events need exactly the same fan-out as the initial load.
     */
    fun threadsForUserUpdate(userId: String): List<String> = ThreadScope(
        openDmThreadIds = _users.value.values.filter { it.state.open }.mapTo(HashSet()) { it.id },
        groups = _groups.value.values.toList(),
        myId = currentUserId,
    ).threadsFor(userId)

    private class ThreadScope(
        private val openDmThreadIds: Set<String>,
        private val groups: List<Group>,
        private val myId: String,
    ) {
        fun threadsFor(userId: String): List<String> {
            // Our own name and avatar are visible everywhere, so the row goes
            // everywhere. Someone else's only shows where we can see them.
            if (userId == myId) return openDmThreadIds.toList() + groups.map { it.id }
            return buildList {
                if (userId in openDmThreadIds) add(userId)
                groups.forEach { group ->
                    if (group.users.any { it.id == userId }) add(group.id)
                }
            }
        }
    }
}

/**
 * Thirteen of the group updates - admin promote/demote, the six
 * restrict/unrestrict variants, block, image change and the three invite
 * outcomes - are the same struct on the wire and differ only by which callback
 * delivered them.
 */
private fun GroupActorUpdate.toSystemEvent(kind: SystemEventKind): ConversationItem.SystemEvent =
    ConversationItem.SystemEvent(
        id = id,
        threadId = thread,
        timestamp = timestamp,
        kind = kind,
        actorId = actor,
        targetUserId = userId.ifEmpty { null },
        seen = seen,
    )

private const val TAG = "BounceRepository"
