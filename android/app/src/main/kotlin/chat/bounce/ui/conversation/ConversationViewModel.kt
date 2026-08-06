package chat.bounce.ui.conversation

import android.content.Context
import android.net.Uri
import android.util.Log
import android.os.SystemClock
import android.provider.OpenableColumns
import androidx.annotation.StringRes
import androidx.documentfile.provider.DocumentFile
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.data.ConversationItem
import chat.bounce.data.ThreadSummary
import chat.bounce.engine.EngineClient
import chat.bounce.engine.EngineHolder
import chat.bounce.engine.Group
import chat.bounce.engine.OutgoingAttachment
import chat.bounce.engine.User
import chat.bounce.goengine.Goengine
import chat.bounce.notifications.BounceMessageNotifier
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
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import java.io.File
import java.util.UUID

/**
 * State and engine plumbing for one open conversation.
 *
 * The engine treats "which thread is on screen" as first-class state: it drives
 * connection priority, read state, and whether a message is worth a
 * notification. That means this ViewModel is not a passive projection - opening
 * and closing it are engine transactions, and [setScrolledDown] is the signal
 * the core uses to decide that a message you are already looking at should not
 * buzz your phone.
 *
 * Assumptions about the data layer (owned by the data agent), stated here so a
 * mismatch is a one-line fix rather than a hunt:
 *
 *   object ChatRepository {
 *       val profile: StateFlow<User?>
 *       val users: StateFlow<Map<String, User>>          // by user UUID
 *       val groups: StateFlow<Map<String, Group>>        // by group UUID
 *       val threads: StateFlow<List<ThreadSummary>>
 *       val messagesByThread: StateFlow<Map<String, List<ConversationItem>>>  // oldest first
 *       val typing: StateFlow<Map<String, Set<String>>>  // threadId -> user UUIDs
 *       val drafts: StateFlow<Map<String, String>>       // threadId -> text
 *       val fileProgress: StateFlow<Map<String, Double>> // fileId -> 0..1
 *   }
 *   ThreadSummary: id, name, isGroup, unreadCount, online
 *   ConversationItem.Message: id, author, writtenAt, text, seen, undeliverable,
 *       expiresAt, imageAttachments, fileAttachments, readReceipts, deliveredTo
 *   ConversationItem.SystemEvent: see SystemRow.kt
 */
class ConversationViewModel(
    private val threadId: String,
    context: Context,
) : ViewModel() {

    private val appContext = context.applicationContext

    /** Text the user has typed here, which always wins over a synced draft. */
    private val localDraft = MutableStateFlow("")
    private val pending = MutableStateFlow<List<PendingAttachment>>(emptyList())

    private var draftPushJob: Job? = null
    private var lastLocalEdit = 0L
    private var lastDraftPublish = 0L
    private var lastTypingSignal = 0L
    private var lastScrolledDown: Boolean? = null
    private val markedRead = mutableSetOf<String>()

    /**
     * onCleared() runs after viewModelScope is cancelled, so the "no thread is
     * open" hand-off needs a scope that outlives this object.
     */
    private val teardownScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /**
     * Groups are the only threads whose id appears in the group map, so this is
     * both the thread-kind test and the routing switch for every paired
     * DM/group engine call.
     */
    private val isGroup: Boolean get() = ChatRepository.groups.value.containsKey(threadId)

    private val repoSlice = combine(
        ChatRepository.messagesByThread,
        ChatRepository.threads,
        ChatRepository.users,
        ChatRepository.groups,
        ChatRepository.profile,
    ) { messages, threads, users, groups, profile ->
        RepoSlice(
            items = messages[threadId].orEmpty(),
            summary = threads.firstOrNull { it.id == threadId },
            users = users,
            group = groups[threadId],
            me = profile?.id.orEmpty(),
            profile = profile,
        )
    }

    private val localSlice = combine(
        ChatRepository.typing,
        ChatRepository.fileProgress,
        localDraft,
        pending,
    ) { typing, progress, draft, attachments ->
        LocalSlice(typing[threadId].orEmpty(), progress, draft, attachments)
    }

    val uiState: StateFlow<ConversationUiState> = combine(repoSlice, localSlice) { repo, local ->
        build(repo, local)
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS),
        initialValue = ConversationUiState(threadId = threadId),
    )

    init {
        activeThreadId = threadId
        // Opening a thread reads everything in it. Done here as well as in the
        // engine call below so the badge clears immediately rather than waiting
        // for a round trip that never reports back.
        ChatRepository.markThreadRead(threadId)
        viewModelScope.launch {
            val client = engine() ?: return@launch
            dismissThreadNotification()
            client.setActiveThread(threadId)
            val now = System.currentTimeMillis() / 1000
            // Recorded locally too: the engine's setters write its database and
            // emit nothing, so without this the inbox would not reorder a
            // drafted thread until the next launch.
            ChatRepository.setLastOpened(threadId, now)
            if (isGroup) {
                client.groupConnectionDesired(threadId)
                client.setGroupLastOpened(threadId, now)
                client.markAllGroupMessagesAsRead(threadId)
            } else {
                client.userConnectionDesired(threadId)
                client.setDMLastOpened(threadId, now)
                client.markAllDirectMessagesAsRead(threadId)
            }
        }

        viewModelScope.launch {
            ChatRepository.drafts
                .map { it[threadId].orEmpty() }
                .distinctUntilChanged()
                .collect { synced ->
                    // Drafts replicate across devices. Adopting one while the
                    // user is mid-sentence would yank the text out from under
                    // them, so a synced draft only lands during a lull.
                    if (System.currentTimeMillis() - lastLocalEdit > DRAFT_ADOPT_QUIET_MS) {
                        localDraft.value = synced
                    }
                }
        }
    }

    override fun onCleared() {
        val text = localDraft.value
        draftPushJob?.cancel()
        // Synchronously, so leaving the screen mid-keystroke cannot strand a
        // draft that the debounce had not published yet.
        ChatRepository.setDraft(threadId, text)
        teardownScope.launch {
            val client = engine() ?: return@launch
            client.updateDraft(threadId, text)
            // Navigation destroys the outgoing entry after the incoming one is
            // created, so blindly clearing here would wipe the thread the user
            // just opened.
            if (activeThreadId == threadId) {
                activeThreadId = ""
                client.setActiveThread("")
            }
        }
    }

    // --- composing ---------------------------------------------------------

    fun updateDraft(text: String) {
        localDraft.value = text
        lastLocalEdit = System.currentTimeMillis()
        signalTyping()

        // Trailing debounce with a ceiling. Rescheduling on every keystroke means
        // a fast typist never reaches the delay and the draft is never published
        // at all, so the wait also shrinks as time since the last publish grows
        // and reaches zero at DRAFT_MAX_INTERVAL_MS. Typing pauses publish
        // promptly; sustained typing publishes on a steady beat.
        draftPushJob?.cancel()
        val sincePublish = System.currentTimeMillis() - lastDraftPublish
        val wait = minOf(
            DRAFT_DEBOUNCE_MS,
            (DRAFT_MAX_INTERVAL_MS - sincePublish).coerceAtLeast(0L),
        )
        draftPushJob = viewModelScope.launch {
            delay(wait)
            publishDraft(localDraft.value)
        }
    }

    /**
     * Hands the draft to the rest of the app.
     *
     * The repository write is not an optimistic guess at what the engine will
     * say - the engine never says anything. UpdateDraft caches and replicates,
     * and the Draft event only ever arrives from *another* device, so a draft
     * typed here reaches this device's own thread list only if we put it there.
     *
     * Batched onto the same schedule as the engine call rather than done per
     * keystroke because ChatRepository.threads is SharingStarted.Eagerly, so
     * every write rebuilds every ThreadSummary whether the inbox is on screen or
     * not.
     */
    private suspend fun publishDraft(text: String) {
        lastDraftPublish = System.currentTimeMillis()
        ChatRepository.setDraft(threadId, text)
        engine()?.updateDraft(threadId, text)
    }

    fun send(text: String, attachments: List<PendingAttachment>) {
        val body = text.trim().limitCodePoints(MAX_CHARACTERS)
        if (body.isEmpty() && attachments.isEmpty()) return

        val sentIds = attachments.mapTo(HashSet()) { it.id }
        localDraft.value = ""
        pending.update { current -> current.filterNot { it.id in sentIds } }
        draftPushJob?.cancel()
        // The text just became a message, so the row must stop advertising it as
        // unsent. Cleared here rather than left to the engine round trip, which
        // never reports back.
        ChatRepository.setDraft(threadId, "")
        // Raised here rather than after the engine call: this thread is about to
        // become the newest in the list, and the request has to be standing by
        // the time the user backs out - which can happen before a blocking send
        // returns.
        ChatRepository.requestThreadListScrollToTop()

        viewModelScope.launch {
            val client = engine() ?: return@launch
            val outgoing = attachments.map { OutgoingAttachment(it.id, it.path, it.name, it.size) }
            if (isGroup) {
                client.sendGroupMessage(threadId, body, outgoing)
            } else {
                client.sendDirectMessage(threadId, body, outgoing)
            }
            client.updateDraft(threadId, "")
        }
    }

    /**
     * Copies each picked item into cacheDir/outgoing/<uuid>. The engine reads
     * attachments from the filesystem by path; a content:// URI is meaningless
     * to it, and the grant would be gone by the time a large upload finished.
     */
    fun addAttachments(uris: List<Uri>) {
        if (uris.isEmpty()) return
        viewModelScope.launch(Dispatchers.IO) {
            val copied = uris.mapNotNull { copyToOutgoing(it) }
            if (copied.isNotEmpty()) pending.update { it + copied }
        }
    }

    fun removeAttachment(id: String) {
        val removed = pending.value.firstOrNull { it.id == id }
        pending.update { current -> current.filterNot { it.id == id } }
        // The staged copy exists only for this composer, so drop it now rather
        // than leaving it for cache eviction.
        if (removed != null) teardownScope.launch { runCatching { File(removed.path).delete() } }
    }

    // --- reading -----------------------------------------------------------

    fun markRead(itemId: String) {
        if (!markedRead.add(itemId)) return
        val item = ChatRepository.messagesByThread.value[threadId]
            ?.firstOrNull { it.id == itemId } as? ConversationItem.Message ?: return
        // System rows never count as unread, so only messages are marked.
        if (item.seen) return
        // Locally too, not just in the engine. MarkAsRead persists and broadcasts
        // a read receipt but never loops a MessageSeen event back to this device
        // - that callback only fires for receipts from *other* sync devices - so
        // without this the item stays unseen in memory and the thread list's
        // unread badge only ever grows.
        ChatRepository.markSeen(itemId)
        val frameType = if (isGroup) Goengine.TypeGroupMessage else Goengine.TypeDirectMessage
        viewModelScope.launch { engine()?.markAsRead(itemId, frameType) }
    }

    fun markAllRead() {
        ChatRepository.markThreadRead(threadId)
        viewModelScope.launch {
            val client = engine() ?: return@launch
            dismissThreadNotification()
            if (isGroup) client.markAllGroupMessagesAsRead(threadId)
            else client.markAllDirectMessagesAsRead(threadId)
        }
    }

    /**
     * Opens the DM with a group member and hands the thread ID back to navigate.
     *
     * setOpenDM is what puts the thread in the inbox - a contact you have never
     * messaged has no row until then - and the engine answers it with SetDMState.
     * The callback fires immediately rather than after the engine call, because
     * the conversation screen can build itself from an empty thread and blocking
     * navigation on a bound call would stall the tap for as long as it takes.
     */
    fun openDirectMessageWith(userId: String, onOpened: (String) -> Unit) {
        viewModelScope.launch {
            val client = engine() ?: return@launch
            client.setOpenDM(userId, true)
            client.userConnectionDesired(userId)
        }
        onOpened(userId)
    }

    fun setScrolledDown(value: Boolean) {
        if (lastScrolledDown == value) return
        lastScrolledDown = value
        viewModelScope.launch { engine()?.setScrolledDown(threadId, value) }
    }

    // --- attachments -------------------------------------------------------

    fun downloadFile(fileId: String, name: String) {
        viewModelScope.launch {
            val client = engine() ?: return@launch
            val destination = withContext(Dispatchers.IO) {
                // Must match the files-path "downloads/" entry in file_paths.xml
                // so the saved file can be shared out through the FileProvider.
                val dir = File(appContext.filesDir, "downloads").apply { mkdirs() }
                uniqueFile(dir, name).absolutePath
            }
            client.downloadFileToDisk(fileId, destination)
        }
    }

    /**
     * Copies an attachment the app already holds out to a location the user
     * picked.
     *
     * Deliberately not [downloadFile], which is a different operation despite
     * the similar name: that one asks the engine to fetch a file from peers into
     * the app's own storage so it can be rendered, and lands in filesDir where
     * nothing outside the app can see it. Exporting has to go through a
     * destination Uri from the system picker, because that is the only way an
     * app holding no storage permission can write somewhere the user can find.
     */
    fun exportAttachment(fileId: String, destination: Uri, onResult: (Boolean) -> Unit) {
        viewModelScope.launch {
            val client = engine()
            if (client == null) {
                onResult(false)
                return@launch
            }
            val saved = withContext(Dispatchers.IO) {
                runCatching {
                    appContext.contentResolver.openOutputStream(destination)?.use { out ->
                        val blob = File(client.blobPath(fileId))
                        if (blob.exists()) {
                            blob.inputStream().use { it.copyTo(out) }
                        } else {
                            // Embedded blobs live on disk, but a file that was
                            // seeded rather than embedded may not, so fall back
                            // to asking the engine for the bytes.
                            out.write(client.fileData(fileId))
                        }
                        true
                    } == true
                }.getOrElse {
                    Log.w(TAG, "could not export attachment $fileId", it)
                    false
                }
            }
            onResult(saved)
        }
    }

    /**
     * Exports several attachments into a directory the user picked.
     *
     * A message can carry more than one file, and CreateDocument names exactly
     * one, so asking for a folder once beats putting the user through a picker
     * per attachment. Reports how many of how many landed rather than a bare
     * success: a partial write is the interesting case and silently claiming
     * success would hide it.
     */
    fun exportAttachmentsToTree(
        tree: Uri,
        attachments: List<Pair<String, String>>,
        onResult: (saved: Int, total: Int) -> Unit,
    ) {
        viewModelScope.launch {
            val client = engine()
            if (client == null) {
                onResult(0, attachments.size)
                return@launch
            }
            val saved = withContext(Dispatchers.IO) {
                val dir = DocumentFile.fromTreeUri(appContext, tree)
                if (dir == null) 0 else attachments.count { (id, name) ->
                    runCatching {
                        val doc = dir.createFile(mimeForFileName(name), name) ?: return@runCatching false
                        appContext.contentResolver.openOutputStream(doc.uri)?.use { out ->
                            val blob = File(client.blobPath(id))
                            if (blob.exists()) blob.inputStream().use { it.copyTo(out) }
                            else out.write(client.fileData(id))
                            true
                        } == true
                    }.getOrElse {
                        Log.w(TAG, "could not export attachment $id", it)
                        false
                    }
                }
            }
            onResult(saved, attachments.size)
        }
    }

    fun cancelDownload(fileId: String) {
        viewModelScope.launch { engine()?.cancelDownload(fileId) }
    }

    // --- thread management -------------------------------------------------

    fun setMutedUntil(until: Long) {
        viewModelScope.launch {
            val client = engine() ?: return@launch
            if (isGroup) client.setGroupMutedUntil(threadId, until)
            else client.setDMMutedUntil(threadId, until)
        }
    }

    fun clearHistory() {
        viewModelScope.launch {
            val client = engine() ?: return@launch
            if (isGroup) client.clearGroupChatHistory(threadId)
            else client.clearDMChatHistory(threadId)
        }
    }

    fun setBlocked(blocked: Boolean) {
        viewModelScope.launch {
            val client = engine() ?: return@launch
            when {
                // Blocking a group is permanent - the engine has no unblock.
                isGroup && blocked -> client.blockGroup(threadId)
                isGroup -> Unit
                blocked -> client.blockUser(threadId)
                else -> client.unblockUser(threadId)
            }
        }
    }

    // --- internals ---------------------------------------------------------

    private fun signalTyping() {
        // The core already debounces before it puts a typing frame on the wire;
        // this throttle only keeps us off the JNI boundary on every keystroke.
        val now = SystemClock.elapsedRealtime()
        if (now - lastTypingSignal < TYPING_THROTTLE_MS) return
        lastTypingSignal = now
        viewModelScope.launch {
            val client = engine() ?: return@launch
            if (isGroup) client.typingInGroup(threadId) else client.typingInDirectMessage(threadId)
        }
    }

    /**
     * Takes this thread's notification out of the shade, because reading the
     * messages on this device is what the notification was for.
     *
     * The engine clears a notification itself for a *single* message - MarkAsRead
     * calls clearNotification - but MarkAll*MessagesAsRead does not, and that is
     * what both local read paths use. Hence the asymmetry this fixes: the same
     * messages read on another sync device did clear the notification, because
     * that arrives here as a read receipt and the receipt handler clears.
     *
     * Handled here rather than in the engine because a notification is per
     * thread, so one cancel is the whole job where the engine would have to emit
     * a clear per message. Note this means the desktop client still has the
     * original behaviour.
     *
     * Call sites invoke this only after [engine] has resolved: the notifier is
     * created by the same service that owns the engine, so before that there is
     * nothing to call. A notification left in the shade by a process that has
     * since been reclaimed still cancels correctly - clearThread cancels by id
     * and does not need the in-memory history that was lost with the process.
     */
    private suspend fun dismissThreadNotification() {
        BounceMessageNotifier.current?.clearThread(threadId)
    }

    /**
     * The service may still be starting the engine when a notification deep
     * link lands us here, so calls wait for it rather than being dropped.
     */
    private suspend fun engine(): EngineClient? = withTimeoutOrNull(ENGINE_WAIT_MS) {
        var client = EngineHolder.client
        while (client == null) {
            delay(ENGINE_POLL_MS)
            client = EngineHolder.client
        }
        client
    }

    private fun copyToOutgoing(uri: Uri): PendingAttachment? = runCatching {
        val resolver = appContext.contentResolver
        val displayName = resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)
            ?.use { cursor -> if (cursor.moveToFirst() && !cursor.isNull(0)) cursor.getString(0) else null }
        val name = displayName?.takeIf { it.isNotBlank() }
            ?: uri.lastPathSegment?.substringAfterLast('/')?.takeIf { it.isNotBlank() }
            ?: "attachment"

        val id = UUID.randomUUID().toString()
        val destination = File(File(appContext.cacheDir, "outgoing").apply { mkdirs() }, id)
        resolver.openInputStream(uri)?.use { input ->
            destination.outputStream().use { output -> input.copyTo(output) }
        } ?: return@runCatching null

        PendingAttachment(
            id = id,
            path = destination.absolutePath,
            name = name,
            size = destination.length(),
            isImage = resolver.getType(uri)?.startsWith("image/") == true,
        )
    }.getOrNull()

    private fun uniqueFile(dir: File, name: String): File {
        val safe = name.replace(Regex("[/\\\\:*?\"<>|]"), "_").ifBlank { "download" }
        val base = safe.substringBeforeLast('.', safe)
        val extension = safe.substringAfterLast('.', "")
        var candidate = File(dir, safe)
        var index = 1
        while (candidate.exists()) {
            val suffix = if (extension.isEmpty()) "" else ".$extension"
            candidate = File(dir, "$base ($index)$suffix")
            index++
        }
        return candidate
    }

    private fun build(repo: RepoSlice, local: LocalSlice): ConversationUiState {
        val group = repo.group
        val user = repo.users[threadId]
        val isGroupThread = repo.summary?.isGroup ?: (group != null)
        val amAdmin = group != null && repo.me in group.admins
        val amMember = group == null || group.users.any { it.id == repo.me }

        val composer = when {
            group != null && group.isPendingInvite -> ComposerState.Disabled(R.string.conv_composer_invite_pending)
            group != null && !amMember -> ComposerState.Disabled(R.string.conv_composer_removed)
            group != null && group.restrictPosting && !amAdmin -> ComposerState.Disabled(R.string.conv_composer_restricted)
            user?.blocked == true -> ComposerState.Disabled(R.string.conv_composer_blocked)
            else -> ComposerState.Enabled
        }

        val mutedUntil = group?.mutedUntil ?: user?.state?.mutedUntil ?: 0L
        val typing = local.typing - repo.me
        val isSelf = threadId == repo.me && repo.me.isNotEmpty()
        val self = repo.profile?.takeIf { isSelf }

        return ConversationUiState(
            threadId = threadId,
            loading = repo.summary == null && group == null && user == null && !isSelf,
            // `self` first: a note-to-self thread has no entry in `users` -
            // InitialState.Users is queried with `profile = false` - and no
            // summary at all until it has messages, so without the profile
            // fallback the header renders blank.
            title = self?.displayName ?: repo.summary?.name ?: group?.name ?: user?.displayName.orEmpty(),
            isGroup = isGroupThread,
            avatarImages = self?.images ?: group?.images ?: user?.images ?: emptyList(),
            items = repo.items,
            users = repo.users,
            currentUserId = repo.me,
            isSelfThread = isSelf,
            online = repo.summary?.online == true,
            memberCount = group?.users?.size ?: 0,
            unreadCount = repo.summary?.unreadCount ?: 0,
            muted = mutedUntil == Goengine.MutedForever || mutedUntil > System.currentTimeMillis() / 1000,
            blocked = user?.blocked == true,
            typingNames = typing.mapNotNull { repo.users[it]?.displayName?.takeIf(String::isNotBlank) },
            draft = local.draft,
            pendingAttachments = local.pending,
            fileProgress = local.fileProgress,
            composer = composer,
        )
    }

    private data class RepoSlice(
        val items: List<ConversationItem>,
        val summary: ThreadSummary?,
        val users: Map<String, User>,
        val group: Group?,
        val me: String,
        val profile: User?,
    )

    private data class LocalSlice(
        val typing: Set<String>,
        val fileProgress: Map<String, Double>,
        val draft: String,
        val pending: List<PendingAttachment>,
    )

    companion object {
        val MAX_CHARACTERS: Int = Goengine.MaximumMessageCharacters.toInt()

        private const val DRAFT_DEBOUNCE_MS = 300L

        /**
         * Longest a draft can go unpublished while the user keeps typing.
         * UpdateDraft is cheap on the engine side - marshal, sign, cache, no
         * database write - so this is about bounding how much is lost if the
         * process dies, not about protecting the engine.
         */
        private const val DRAFT_MAX_INTERVAL_MS = 2_000L
        private const val DRAFT_ADOPT_QUIET_MS = 3_000L
        private const val TYPING_THROTTLE_MS = 4_000L
        private const val STOP_TIMEOUT_MS = 5_000L
        private const val ENGINE_WAIT_MS = 30_000L
        private const val ENGINE_POLL_MS = 100L

        /**
         * The thread this process last told the engine about. Shared because
         * two ConversationViewModels overlap during a thread-to-thread
         * navigation and only the newest one owns the active-thread slot.
         */
        @Volatile
        private var activeThreadId: String = ""

        fun factory(threadId: String, context: Context): ViewModelProvider.Factory = viewModelFactory {
            initializer { ConversationViewModel(threadId, context) }
        }
    }
}

/** A file the user picked but has not sent yet, already staged on disk. */
data class PendingAttachment(
    val id: String,
    val path: String,
    val name: String,
    val size: Long,
    val isImage: Boolean,
)

sealed interface ComposerState {
    data object Enabled : ComposerState

    data class Disabled(@StringRes val reason: Int) : ComposerState
}

data class ConversationUiState(
    val threadId: String = "",
    val loading: Boolean = true,
    val title: String = "",
    val isGroup: Boolean = false,
    val avatarImages: List<String> = emptyList(),
    val items: List<ConversationItem> = emptyList(),
    val users: Map<String, User> = emptyMap(),
    val currentUserId: String = "",
    val isSelfThread: Boolean = false,
    val online: Boolean = false,
    val memberCount: Int = 0,
    val unreadCount: Int = 0,
    val muted: Boolean = false,
    val blocked: Boolean = false,
    val typingNames: List<String> = emptyList(),
    val draft: String = "",
    val pendingAttachments: List<PendingAttachment> = emptyList(),
    val fileProgress: Map<String, Double> = emptyMap(),
    val composer: ComposerState = ComposerState.Enabled,
)

/** Truncates on code points, matching the engine's rune-count limit. */
internal fun String.limitCodePoints(max: Int): String =
    if (codePointCount(0, length) <= max) this else substring(0, offsetByCodePoints(0, max))

private const val TAG = "BounceConversation"

/**
 * Best-effort MIME from the filename. SAF needs a type to create a document;
 * getting it wrong only affects which app claims the file by default, so an
 * unknown extension falls back to a generic binary rather than guessing.
 */
private fun mimeForFileName(name: String): String =
    android.webkit.MimeTypeMap.getSingleton()
        .getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase())
        ?: "application/octet-stream"
