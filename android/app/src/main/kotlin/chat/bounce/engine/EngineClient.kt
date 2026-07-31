package chat.bounce.engine

import android.os.SystemClock
import android.util.Log
import chat.bounce.goengine.Engine
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json

/**
 * Suspending facade over the gomobile [Engine].
 *
 * Every bound call is a synchronous JNI-to-cgo call that blocks the calling
 * thread until Go returns, and there is no async variant. Rather than audit
 * which of the ~80 methods are cheap, the rule here is uniform: the whole
 * engine is Dispatchers.IO. That is correct today and stays correct when a
 * method that looks like a setter later grows a database write.
 *
 * [blobPath] is the single documented exception - it is string concatenation
 * in Go with no engine call - so it is not suspending.
 */
class EngineClient(
    private val engine: Engine,
    private val io: CoroutineDispatcher = Dispatchers.IO,
) {
    /**
     * Every engine call funnels through here, so this is the one place that can
     * see how long the JNI hop actually takes.
     *
     * Worth timing because the cost is invisible from Kotlin: each call is a
     * synchronous hop into Go, and the engine opens SQLite with
     * SetMaxOpenConns(1) - so every database operation in the whole process
     * serialises through a single connection, and a call that is normally
     * instant can block behind an unrelated one.
     */
    private suspend fun <T> call(name: String, block: (Engine) -> T): T = withContext(io) {
        val startedAt = SystemClock.elapsedRealtime()
        try {
            block(engine)
        } finally {
            val elapsed = SystemClock.elapsedRealtime() - startedAt
            if (elapsed >= SLOW_CALL_MS) Log.w(TAG, "engine call $name took ${elapsed}ms")
        }
    }

    val isReady: Boolean get() = engine.ready()

    // --- profile -----------------------------------------------------------

    suspend fun setProfile(name: String, image: ByteArray, deviceName: String) =
        call("setProfile") { it.setProfile(name, image, deviceName) }

    suspend fun updateProfileName(name: String) = call("updateProfileName") { it.updateProfileName(name) }

    suspend fun updateProfileImage(image: ByteArray) = call("updateProfileImage") { it.updateProfileImage(image) }

    suspend fun currentUserId(): String = call("currentUserId") { it.currentUserID() }

    // --- pairing and contacts ----------------------------------------------

    suspend fun newAddUserString(): String = call("newAddUserString") { it.newAddUserString }

    suspend fun requestToAddUser(offer: String) = call("requestToAddUser") { it.requestToAddUser(offer) }

    suspend fun newSyncString(): String = call("newSyncString") { it.newSyncString }

    suspend fun requestToSync(data: String) = call("requestToSync") { it.requestToSync(data) }

    suspend fun requestToManageEncryptedDevice(data: String) =
        call("requestToManageEncryptedDevice") { it.requestToManageEncryptedDevice(data) }

    // --- devices -----------------------------------------------------------

    suspend fun renameDevice(deviceId: String, name: String) = call("renameDevice") { it.renameDevice(deviceId, name) }

    suspend fun revokeDevice(deviceId: String) = call("revokeDevice") { it.revokeDevice(deviceId) }

    suspend fun currentDeviceActive() = call("currentDeviceActive") { it.currentDeviceActive() }

    // --- users -------------------------------------------------------------

    suspend fun aliasUser(userId: String, alias: String) = call("aliasUser") { it.aliasUser(userId, alias) }

    suspend fun setUserNotes(userId: String, notes: String) = call("setUserNotes") { it.setUserNotes(userId, notes) }

    suspend fun blockUser(userId: String) = call("blockUser") { it.blockUser(userId) }

    suspend fun unblockUser(userId: String) = call("unblockUser") { it.unblockUser(userId) }

    suspend fun userConnectionDesired(userId: String) = call("userConnectionDesired") { it.userConnectionDesired(userId) }

    suspend fun groupConnectionDesired(groupId: String) = call("groupConnectionDesired") { it.groupConnectionDesired(groupId) }

    // --- DM threads --------------------------------------------------------

    suspend fun setOpenDM(userId: String, open: Boolean) = call("setOpenDM") { it.setOpenDM(userId, open) }

    suspend fun setDMRetention(userId: String, retention: Long) =
        call("setDMRetention") { it.setDMRetention(userId, retention) }

    suspend fun setDMMutedUntil(userId: String, mutedUntil: Long) =
        call("setDMMutedUntil") { it.setDMMutedUntil(userId, mutedUntil) }

    suspend fun setDMReadReceiptSettings(userId: String, override: Boolean, enabled: Boolean) =
        call("setDMReadReceiptSettings") { it.setDMReadReceiptSettings(userId, override, enabled) }

    suspend fun setDMTypingIndicatorSettings(userId: String, override: Boolean, enabled: Boolean) =
        call("setDMTypingIndicatorSettings") { it.setDMTypingIndicatorSettings(userId, override, enabled) }

    suspend fun setDMLastOpened(userId: String, timestamp: Long) =
        call("setDMLastOpened") { it.setDMLastOpened(userId, timestamp) }

    suspend fun clearDMChatHistory(userId: String) = call("clearDMChatHistory") { it.clearDMChatHistory(userId) }

    suspend fun markAllDirectMessagesAsRead(userId: String) =
        call("markAllDirectMessagesAsRead") { it.markAllDirectMessagesAsRead(userId) }

    suspend fun typingInDirectMessage(userId: String) = call("typingInDirectMessage") { it.typingInDirectMessage(userId) }

    // --- groups ------------------------------------------------------------

    suspend fun createGroup(newGroup: NewGroup, image: ByteArray = ByteArray(0)) =
        call("createGroup") { it.createGroup(json.encodeToString(newGroup), image) }

    suspend fun renameGroup(groupId: String, name: String) = call("renameGroup") { it.renameGroup(groupId, name) }

    suspend fun setGroupImage(groupId: String, image: ByteArray) =
        call("setGroupImage") { it.setGroupImage(groupId, image) }

    suspend fun deleteGroup(groupId: String) = call("deleteGroup") { it.deleteGroup(groupId) }

    suspend fun blockGroup(groupId: String) = call("blockGroup") { it.blockGroup(groupId) }

    suspend fun acceptInvite(groupId: String) = call("acceptInvite") { it.acceptInvite(groupId) }

    suspend fun rejectInvite(groupId: String) = call("rejectInvite") { it.rejectInvite(groupId) }

    suspend fun inviteUserToGroup(groupId: String, userId: String) =
        call("inviteUserToGroup") { it.inviteUserToGroup(groupId, userId) }

    suspend fun revokeInvite(groupId: String, userId: String) =
        call("revokeInvite") { it.revokeInvite(groupId, userId) }

    suspend fun removeUserFromGroup(groupId: String, userId: String) =
        call("removeUserFromGroup") { it.removeUserFromGroup(groupId, userId) }

    suspend fun promoteGroupAdmin(groupId: String, userId: String) =
        call("promoteGroupAdmin") { it.promoteGroupAdmin(groupId, userId) }

    suspend fun demoteGroupAdmin(groupId: String, userId: String) =
        call("demoteGroupAdmin") { it.demoteGroupAdmin(groupId, userId) }

    suspend fun restrictUserManagement(groupId: String) = call("restrictUserManagement") { it.restrictUserManagement(groupId) }

    suspend fun unrestrictUserManagement(groupId: String) =
        call("unrestrictUserManagement") { it.unrestrictUserManagement(groupId) }

    suspend fun restrictGroupEdits(groupId: String) = call("restrictGroupEdits") { it.restrictGroupEdits(groupId) }

    suspend fun unrestrictGroupEdits(groupId: String) = call("unrestrictGroupEdits") { it.unrestrictGroupEdits(groupId) }

    suspend fun restrictPosting(groupId: String) = call("restrictPosting") { it.restrictPosting(groupId) }

    suspend fun unrestrictPosting(groupId: String) = call("unrestrictPosting") { it.unrestrictPosting(groupId) }

    suspend fun setGroupRetention(groupId: String, retention: Long) =
        call("setGroupRetention") { it.setGroupRetention(groupId, retention) }

    suspend fun setGroupMutedUntil(groupId: String, mutedUntil: Long) =
        call("setGroupMutedUntil") { it.setGroupMutedUntil(groupId, mutedUntil) }

    suspend fun setGroupReadReceiptSettings(groupId: String, override: Boolean, enabled: Boolean) =
        call("setGroupReadReceiptSettings") { it.setGroupReadReceiptSettings(groupId, override, enabled) }

    suspend fun setGroupTypingIndicatorSettings(groupId: String, override: Boolean, enabled: Boolean) =
        call("setGroupTypingIndicatorSettings") { it.setGroupTypingIndicatorSettings(groupId, override, enabled) }

    suspend fun setGroupLastOpened(groupId: String, timestamp: Long) =
        call("setGroupLastOpened") { it.setGroupLastOpened(groupId, timestamp) }

    suspend fun clearGroupChatHistory(groupId: String) = call("clearGroupChatHistory") { it.clearGroupChatHistory(groupId) }

    suspend fun markAllGroupMessagesAsRead(groupId: String) =
        call("markAllGroupMessagesAsRead") { it.markAllGroupMessagesAsRead(groupId) }

    suspend fun typingInGroup(groupId: String) = call("typingInGroup") { it.typingInGroup(groupId) }

    // --- messages ----------------------------------------------------------

    suspend fun sendDirectMessage(
        threadId: String,
        text: String,
        attachments: List<OutgoingAttachment> = emptyList(),
    ) = call("sendDirectMessage") { it.sendDirectMessage(threadId, text, encodeAttachments(attachments)) }

    suspend fun sendGroupMessage(
        threadId: String,
        text: String,
        attachments: List<OutgoingAttachment> = emptyList(),
    ) = call("sendGroupMessage") { it.sendGroupMessage(threadId, text, encodeAttachments(attachments)) }

    private fun encodeAttachments(attachments: List<OutgoingAttachment>): String =
        if (attachments.isEmpty()) "" else json.encodeToString(attachments)

    // --- state -------------------------------------------------------------

    suspend fun initialState(): InitialState =
        call("initialState") { json.decodeFromString<InitialState>(it.initialState) }

    suspend fun dmHistory(userId: String): InitialState =
        call("dmHistory") { json.decodeFromString<InitialState>(it.getDMHistory(userId)) }

    suspend fun networkOnline(): Boolean = call("networkOnline") { it.networkOnline() }

    // --- read state, drafts, presence hints --------------------------------

    suspend fun markAsRead(id: String, frameType: String) = call("markAsRead") { it.markAsRead(id, frameType) }

    /** Pass an empty string to mean "no thread is open". */
    suspend fun setActiveThread(threadId: String) = call("setActiveThread") { it.setActiveThread(threadId) }

    suspend fun setScrolledDown(threadId: String, value: Boolean) =
        call("setScrolledDown") { it.setScrolledDown(threadId, value) }

    suspend fun updateDraft(threadId: String, text: String) = call("updateDraft") { it.updateDraft(threadId, text) }

    suspend fun setForeground(foreground: Boolean) = call("setForeground") { it.setForeground(foreground) }

    suspend fun setNotificationIcon(threadId: String, icon: ByteArray) =
        call("setNotificationIcon") { it.setNotificationIcon(threadId, icon) }

    // --- settings ----------------------------------------------------------

    suspend fun setAutoJoinGroups(value: Int) = call("setAutoJoinGroups") { it.setAutoJoinGroups(value) }

    suspend fun setNewDMRetention(value: Long) = call("setNewDMRetention") { it.setNewDMRetention(value) }

    suspend fun setNewGroupRetention(value: Long) = call("setNewGroupRetention") { it.setNewGroupRetention(value) }

    suspend fun setNewGroupRestrictUserManagement(value: Boolean) =
        call("setNewGroupRestrictUserManagement") { it.setNewGroupRestrictUserManagement(value) }

    suspend fun setNewGroupRestrictGroupEdits(value: Boolean) =
        call("setNewGroupRestrictGroupEdits") { it.setNewGroupRestrictGroupEdits(value) }

    suspend fun setNewGroupRestrictPosting(value: Boolean) =
        call("setNewGroupRestrictPosting") { it.setNewGroupRestrictPosting(value) }

    suspend fun setReadReceiptsByDefault(value: Boolean) = call("setReadReceiptsByDefault") { it.setReadReceiptsByDefault(value) }

    suspend fun setTypingIndicatorsByDefault(value: Boolean) =
        call("setTypingIndicatorsByDefault") { it.setTypingIndicatorsByDefault(value) }

    // --- files -------------------------------------------------------------

    /** Not suspending: pure string concatenation in Go, safe from any thread. */
    fun blobPath(fileId: String): String = engine.blobPath(fileId)

    suspend fun fileData(fileId: String): ByteArray = call("fileData") { it.getFileData(fileId) }

    suspend fun fileDownloaded(fileId: String): Boolean = call("fileDownloaded") { it.fileDownloaded(fileId) }

    suspend fun fileEmbedded(fileId: String): Boolean = call("fileEmbedded") { it.fileEmbedded(fileId) }

    suspend fun fileWanted(fileId: String): Boolean = call("fileWanted") { it.fileWanted(fileId) }

    suspend fun downloadFileToDisk(fileId: String, destination: String) =
        call("downloadFileToDisk") { it.downloadFileToDisk(fileId, destination) }

    suspend fun cancelDownload(fileId: String) = call("cancelDownload") { it.cancelDownload(fileId) }

    companion object {

        private const val TAG = "BounceEngineCall"

        /**
         * Anything at or over this is worth seeing. Well above a healthy call,
         * which is sub-millisecond, and well under a stall a user would notice.
         */
        private const val SLOW_CALL_MS = 100L

        /**
         * ignoreUnknownKeys so a Go-side DTO that gains a field degrades to a
         * no-op rather than throwing; isLenient stays off so genuine shape
         * mismatches still surface.
         *
         * coerceInputValues is load-bearing, not defensive: Go marshals a nil
         * slice as JSON `null`, and the engine routinely emits messages with
         * unset ReadReceipts/DeliveredTo (chat/direct_message.go builds
         * DisplaySentDirectMessage without them). Without coercion kotlinx
         * rejects an explicit null for a non-nullable List even when it has a
         * default, and every sent message would fail to parse.
         */
        val json = Json {
            ignoreUnknownKeys = true
            encodeDefaults = true
            coerceInputValues = true
        }
    }
}
