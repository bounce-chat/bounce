package chat.bounce.data

import android.util.Log
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.JsonElement
import chat.bounce.engine.BulkUpdate
import chat.bounce.engine.DMState
import chat.bounce.engine.Device
import chat.bounce.engine.DirectMessage
import chat.bounce.engine.Draft
import chat.bounce.engine.EngineBridge
import chat.bounce.engine.EngineClient
import chat.bounce.engine.EngineSignal
import chat.bounce.engine.Group
import chat.bounce.engine.GroupActorUpdate
import chat.bounce.engine.GroupMessage
import chat.bounce.engine.ReadReceipt
import chat.bounce.engine.Settings
import chat.bounce.engine.UpdateDMClearHistory
import chat.bounce.engine.UpdateDMRetention
import chat.bounce.engine.UpdateDMSetAlias
import chat.bounce.engine.UpdateGroupClearHistory
import chat.bounce.engine.UpdateGroupInviteUser
import chat.bounce.engine.UpdateGroupName
import chat.bounce.engine.UpdateGroupRemoveUser
import chat.bounce.engine.UpdateGroupRetention
import chat.bounce.engine.UpdateUserUpdateImage
import chat.bounce.engine.UpdateUserUpdateName
import chat.bounce.engine.User
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.Json

/**
 * The one and only consumer of [EngineBridge.signals].
 *
 * There must never be a second collector. The channel is a channel, not a
 * broadcast: a second consumer would steal half the events at random. More
 * importantly the ordering guarantee that makes notifications work - the engine
 * emits DisplayDirectMessage before it asks for the matching notification -
 * only holds if one loop applies both in sequence, which is exactly what this
 * class does.
 *
 * Runs on Dispatchers.Default: the work is JSON parsing and immutable-collection
 * rebuilding, both CPU-bound, and none of it may run on the main thread.
 *
 * Call ChatRepository.load first. Incoming messages are tagged outgoing by
 * comparing their author to the local profile ID, which is only known once the
 * snapshot has been applied; starting the pump against an empty repository
 * would render our own first messages as though someone else sent them.
 */
class EngineEventPump(
    private val client: EngineClient,
    private val bridge: EngineBridge,
    private val repo: ChatRepository,
    private val notifier: MessageNotifier,
) {

    fun start(scope: CoroutineScope): Job = scope.launch(Dispatchers.Default) {
        for (signal in bridge.signals) {
            try {
                dispatch(signal)
            } catch (cancellation: CancellationException) {
                throw cancellation
            } catch (t: Throwable) {
                // One malformed or unexpected event must not stop the stream; the
                // alternative is a silently dead app that still holds a
                // foreground notification saying it is connected.
                Log.e(TAG, "failed to apply engine signal ${describe(signal)}", t)
            }
        }
        Log.i(TAG, "engine signal stream closed")
    }

    private suspend fun dispatch(signal: EngineSignal) {
        when (signal) {
            is EngineSignal.Raw -> handleRaw(signal.kind, signal.payload)

            is EngineSignal.Typing ->
                repo.setTyping(signal.userId, signal.threadId, signal.typing)

            is EngineSignal.Presence ->
                if (signal.isUser) repo.setUserOnline(signal.id, signal.online)
                else repo.setDeviceOnline(signal.id, signal.online)

            is EngineSignal.Progress ->
                if (signal.isInitialSync) {
                    repo.setInitialSync(InitialSyncState.Progress(signal.fraction))
                } else {
                    repo.setFileProgress(signal.id, signal.fraction)
                }

            // Reached only after every earlier signal has been applied, so the
            // message this refers to is already in the repository.
            is EngineSignal.NotificationPost -> notifier.onNotificationPost(signal)

            is EngineSignal.NotificationClear -> notifier.onNotificationClear(signal.id)
        }
    }

    private suspend fun handleRaw(kind: String, payload: String) {
        when (kind) {
            // --- app and network lifecycle -----------------------------------
            "AnotherDeviceActive" -> repo.setAnotherDeviceActive(true)
            "NoOtherDeviceActive" -> repo.setAnotherDeviceActive(false)
            "Quit" -> repo.emit(RepositoryEffect.EngineQuit)
            "NetworkOnline" -> repo.setNetworkOnline(true)
            "NetworkOffline" -> repo.setNetworkOnline(false)

            // --- initial sync -------------------------------------------------
            "InitialSyncStarting" -> repo.setInitialSync(InitialSyncState.Starting)
            "InitialSyncPreparing" -> repo.setInitialSync(InitialSyncState.Preparing)
            "InitialSyncComplete" -> {
                repo.setInitialSync(InitialSyncState.Complete)
                // A sync brings over users, groups, settings and drafts through
                // paths that emit no per-object callback, so the only correct
                // view afterwards is a fresh snapshot.
                repo.load(client)
            }

            // --- profile and pairing ------------------------------------------
            "ProfileSet" -> {
                val event = decode<ProfileSetPayload>(payload)
                repo.setProfile(event.user)
                repo.upsertDevice(event.device)
                repo.emit(RepositoryEffect.ProfileCreated(event.user.id))
            }

            "NewSyncDeviceAdded" -> repo.emit(RepositoryEffect.SyncDeviceAdded)

            "SyncDeviceRequestAccepted" -> {
                val event = decode<SyncAcceptedPayload>(payload)
                repo.setProfile(event.user)
                event.devices.forEach(repo::upsertDevice)
                repo.emit(RepositoryEffect.SyncDeviceAccepted(event.user.id, event.references))
            }

            "SyncDeviceRequestRejected" ->
                repo.emit(RepositoryEffect.SyncDeviceRejected(decode<PeerPayload>(payload).peer))

            "AddUserRequestRejected" ->
                repo.emit(RepositoryEffect.AddUserRejected(decode<PeerPayload>(payload).peer))

            // --- devices --------------------------------------------------------
            "DeviceAdded" -> repo.upsertDevice(decode<Device>(payload))

            "DeviceRevoked" -> {
                val id = decode<IdPayload>(payload).id
                // Losing our own device row means this install has been cut off
                // from the network for good. Checked before removeDevice, which
                // takes the row the check reads.
                if (repo.devices.value.firstOrNull { it.id == id }?.local == true) {
                    repo.markDeviceRevoked()
                }
                repo.removeDevice(id)
            }

            "DeviceRenamed" -> decode<DeviceNamePayload>(payload).let {
                repo.renameDevice(it.id, it.name)
            }

            "DeviceLastSeen" -> decode<DeviceLastSeenPayload>(payload).let {
                repo.setDeviceLastSeen(it.id, it.lastSeen)
            }

            "EncryptedDeviceAdded" -> repo.emit(RepositoryEffect.EncryptedDeviceAdded)
            "EncryptedDeviceRejected" -> repo.emit(RepositoryEffect.EncryptedDeviceRejected)

            "EncryptedDeviceManagable" -> repo.emit(
                RepositoryEffect.EncryptedDeviceManageable(decode<IdPayload>(payload).id, true)
            )

            "EncryptedDeviceUnmanagable" -> repo.emit(
                RepositoryEffect.EncryptedDeviceManageable(decode<IdPayload>(payload).id, false)
            )

            // --- users ----------------------------------------------------------
            "UserAdded" -> decode<User>(payload).let {
                repo.upsertUser(it)
                repo.emit(RepositoryEffect.UserAdded(it.id))
            }

            "SetUserState" -> repo.upsertUser(decode<User>(payload))

            "SetDMState" -> decode<DMStatePayload>(payload).let {
                repo.setDMState(it.id, it.state)
            }

            "UserNameUpdated" -> handleUserNameUpdated(decode(payload))
            "UserImageUpdated" -> handleUserImageUpdated(decode(payload))
            "UserAliased" -> handleUserAliased(decode(payload))

            // --- messages -------------------------------------------------------
            "DisplayDirectMessage", "DisplaySentDirectMessage" ->
                repo.addItem(decode<DirectMessage>(payload).toConversationItem(repo.currentUserId))

            "DisplayGroupMessage", "DisplaySentGroupMessage" ->
                repo.addItem(decode<GroupMessage>(payload).toConversationItem(repo.currentUserId))

            "DeleteItem" -> repo.deleteItem(decode<IdPayload>(payload).id)
            "MessageSeen" -> repo.markSeen(decode<IdPayload>(payload).id)
            "MarkMessageUndeliverable" -> repo.markUndeliverable(decode<IdPayload>(payload).id)

            "MessageDelivered" -> decode<MessageDeliveredPayload>(payload).let {
                repo.markDelivered(it.messageId, it.userId)
            }

            "ReceivedReadReceipt" -> repo.addReadReceipt(decode<ReadReceipt>(payload))

            // Decoded defensively. kotlinx parses the payload as one document,
            // so a single unparseable message would otherwise take the whole
            // batch - potentially hundreds of messages - down with it, and the
            // engine announces each message to the UI only once per catch-up.
            // Salvaging the rest costs one extra parse attempt on a path that
            // only runs when something is already wrong.
            "CatchUpMessages" -> repo.applyBulkUpdate(decodeBulkUpdate(payload))

            "UpdateDraft" -> decode<Draft>(payload).let { repo.setDraft(it.thread, it.text) }

            // --- DM thread updates ----------------------------------------------
            "DMRetentionChanged" -> decode<UpdateDMRetention>(payload).let {
                repo.addItem(
                    ConversationItem.SystemEvent(
                        id = it.id,
                        threadId = it.thread,
                        timestamp = it.timestamp,
                        kind = SystemEventKind.RETENTION_CHANGED,
                        actorId = it.actor,
                        retention = it.retention,
                        seen = it.seen,
                    )
                )
            }

            "DMChatHistoryCleared" -> decode<UpdateDMClearHistory>(payload).let {
                repo.clearThreadBefore(it.thread, it.clearTime)
                repo.addItem(
                    ConversationItem.SystemEvent(
                        id = it.id,
                        threadId = it.thread,
                        timestamp = it.clearTime,
                        kind = SystemEventKind.HISTORY_CLEARED,
                        actorId = it.actor,
                        seen = it.seen,
                    )
                )
            }

            // --- group membership and lifecycle ----------------------------------
            "OpenNewGroupChat", "SetGroupState" -> repo.upsertGroup(decode<Group>(payload))

            "RemovedFromGroup" -> decode<chat.bounce.engine.RemovedFromGroup>(payload).let {
                repo.removeGroup(it.group)
                repo.emit(RepositoryEffect.RemovedFromGroup(it.group, it.actor))
            }

            "GroupDeleted" -> decode<chat.bounce.engine.GroupDeleted>(payload).let {
                repo.removeGroup(it.group)
                repo.emit(RepositoryEffect.GroupDeleted(it.group, it.actor))
            }

            "RollbackGroup" -> decode<IdPayload>(payload).let {
                repo.removeGroup(it.id)
                repo.emit(RepositoryEffect.GroupRolledBack(it.id))
            }

            // --- group updates that render as system rows -------------------------
            "RenameGroup" -> decode<UpdateGroupName>(payload).let {
                repo.addItem(
                    ConversationItem.SystemEvent(
                        id = it.id,
                        threadId = it.thread,
                        timestamp = it.timestamp,
                        kind = SystemEventKind.GROUP_RENAMED,
                        actorId = it.actor,
                        name = it.name,
                        seen = it.seen,
                    )
                )
            }

            "GroupRetentionChanged" -> decode<UpdateGroupRetention>(payload).let {
                repo.addItem(
                    ConversationItem.SystemEvent(
                        id = it.id,
                        threadId = it.thread,
                        timestamp = it.timestamp,
                        kind = SystemEventKind.RETENTION_CHANGED,
                        actorId = it.actor,
                        retention = it.retention,
                        seen = it.seen,
                    )
                )
            }

            "GroupChatHistoryCleared" -> decode<UpdateGroupClearHistory>(payload).let {
                repo.clearThreadBefore(it.thread, it.clearTime)
                repo.addItem(
                    ConversationItem.SystemEvent(
                        id = it.id,
                        threadId = it.thread,
                        timestamp = it.clearTime,
                        kind = SystemEventKind.HISTORY_CLEARED,
                        actorId = it.actor,
                        seen = it.seen,
                    )
                )
            }

            "InviteUser" -> decode<UpdateGroupInviteUser>(payload).let {
                // The invitee may be a stranger; record them so the row can name
                // them after a restart, when the event has only their ID.
                repo.addUserIfAbsent(it.user)
                repo.addItem(
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
                )
            }

            "RemoveUser" -> decode<UpdateGroupRemoveUser>(payload).let {
                repo.addItem(
                    ConversationItem.SystemEvent(
                        id = it.id,
                        threadId = it.thread,
                        timestamp = it.timestamp,
                        kind = SystemEventKind.USER_REMOVED,
                        actorId = it.actor,
                        targetUserId = it.user,
                        seen = it.seen,
                    )
                )
            }

            "AdminPromoted" -> addGroupActorEvent(payload, SystemEventKind.ADMIN_PROMOTED)
            "AdminDemoted" -> addGroupActorEvent(payload, SystemEventKind.ADMIN_DEMOTED)
            "UserManagementRestricted" ->
                addGroupActorEvent(payload, SystemEventKind.USER_MANAGEMENT_RESTRICTED)
            "UserManagementUnrestricted" ->
                addGroupActorEvent(payload, SystemEventKind.USER_MANAGEMENT_UNRESTRICTED)
            "GroupEditsRestricted" ->
                addGroupActorEvent(payload, SystemEventKind.GROUP_EDITS_RESTRICTED)
            "GroupEditsUnrestricted" ->
                addGroupActorEvent(payload, SystemEventKind.GROUP_EDITS_UNRESTRICTED)
            "PostingRestricted" -> addGroupActorEvent(payload, SystemEventKind.POSTING_RESTRICTED)
            "PostingUnrestricted" ->
                addGroupActorEvent(payload, SystemEventKind.POSTING_UNRESTRICTED)
            "UserBlockedGroup" -> addGroupActorEvent(payload, SystemEventKind.USER_BLOCKED_GROUP)
            "UserChangedGroupImage" ->
                addGroupActorEvent(payload, SystemEventKind.GROUP_IMAGE_CHANGED)
            "GroupInviteRevoked" -> addGroupActorEvent(payload, SystemEventKind.INVITE_REVOKED)
            "GroupInviteAccepted" -> addGroupActorEvent(payload, SystemEventKind.INVITE_ACCEPTED)
            "GroupInviteRejected" -> addGroupActorEvent(payload, SystemEventKind.INVITE_REJECTED)

            // --- settings and files ------------------------------------------------
            "SetSettings" -> repo.setSettings(decode<Settings>(payload))
            "FileCompleted" -> repo.fileCompleted(decode<IdPayload>(payload).id)

            else -> Log.w(TAG, "ignoring unknown engine event kind '$kind'")
        }
    }

    private fun addGroupActorEvent(payload: String, kind: SystemEventKind) {
        val update = decode<GroupActorUpdate>(payload)
        repo.addItem(
            ConversationItem.SystemEvent(
                id = update.id,
                threadId = update.thread,
                timestamp = update.timestamp,
                kind = kind,
                actorId = update.actor,
                targetUserId = update.userId.ifEmpty { null },
                seen = update.seen,
            )
        )
    }

    /**
     * Profile-level changes carry no thread, because they are true everywhere.
     * The row is duplicated into each conversation the user is visible in, which
     * is what the desktop client does; the shared frame ID means DeleteItem
     * still removes every copy.
     */
    private fun handleUserNameUpdated(update: UpdateUserUpdateName) {
        repo.userById(update.user)?.let { repo.upsertUser(it.copy(name = update.name)) }
        repo.addItems(
            repo.threadsForUserUpdate(update.user).map { threadId ->
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
        )
    }

    /**
     * The callback carries no image ID, only "user X changed their picture", so
     * nothing here updates the avatar: the engine emits SetUserState from the
     * same updateUser frame and that snapshot carries the new Images list.
     */
    private fun handleUserImageUpdated(update: UpdateUserUpdateImage) {
        repo.addItems(
            repo.threadsForUserUpdate(update.user).map { threadId ->
                ConversationItem.SystemEvent(
                    id = update.id,
                    threadId = threadId,
                    timestamp = update.timestamp,
                    kind = SystemEventKind.USER_IMAGE_CHANGED,
                    actorId = update.user,
                    targetUserId = update.user,
                )
            }
        )
    }

    private fun handleUserAliased(update: UpdateDMSetAlias) {
        repo.userById(update.user)?.let { repo.upsertUser(it.copy(alias = update.alias)) }
        repo.addItems(
            repo.threadsForUserUpdate(update.user).map { threadId ->
                ConversationItem.SystemEvent(
                    id = update.id,
                    threadId = threadId,
                    timestamp = update.timestamp,
                    kind = SystemEventKind.ALIAS_SET,
                    actorId = update.user,
                    targetUserId = update.user,
                    name = update.alias,
                )
            }
        )
    }

    private inline fun <reified T> decode(payload: String): T = json.decodeFromString<T>(payload)

    /**
     * Parses a catch-up payload, falling back to per-message parsing.
     *
     * The strict path is the normal one. If it throws, the batch is re-walked
     * message by message so one bad element costs one message instead of the
     * whole catch-up, and the offender is named in the log rather than
     * disappearing into a single "failed to apply" line.
     */
    private fun decodeBulkUpdate(payload: String): BulkUpdate =
        runCatching { decode<CatchUpPayload>(payload).update }.getOrElse { strict ->
            Log.e(TAG, "catch-up payload did not parse as a whole; salvaging per message", strict)
            val root = runCatching { json.parseToJsonElement(payload).jsonObject["update"]?.jsonObject }
                .getOrNull() ?: return BulkUpdate()

            fun <T> salvage(field: String, decodeOne: (JsonElement) -> T): Map<String, List<T>> =
                root[field]?.jsonObject.orEmpty().mapValues { (thread, arr) ->
                    arr.jsonArray.mapNotNull { element ->
                        runCatching { decodeOne(element) }.getOrElse {
                            Log.e(TAG, "dropping one message in thread $thread", it)
                            null
                        }
                    }
                }

            BulkUpdate(
                directMessages = salvage("DirectMessages") { json.decodeFromJsonElement<DirectMessage>(it) },
                groupMessages = salvage("GroupMessages") { json.decodeFromJsonElement<GroupMessage>(it) },
            )
        }


    /** Log without dumping message text into logcat. */
    private fun describe(signal: EngineSignal): String = when (signal) {
        is EngineSignal.Raw -> "Raw(${signal.kind})"
        else -> signal::class.simpleName.orEmpty()
    }

    private companion object {
        const val TAG = "BouncePump"

        /**
         * EngineClient.json, plus coerceInputValues.
         *
         * Go marshals a nil slice as `null`, not `[]`, and several engine DTOs
         * leave slices unset - DisplaySentDirectMessage never populates
         * ReadReceipts or DeliveredTo, so both arrive as an explicit null.
         * Without coercion kotlinx rejects that against a non-nullable
         * List-with-a-default instead of falling back to the default, and every
         * outgoing message would be dropped here as a parse failure.
         */
        val json = Json(EngineClient.json) { coerceInputValues = true }
    }
}

// --- envelope payloads ---------------------------------------------------------
//
// goengine/events.go wraps every callback whose arguments are not a single chat
// struct, so that Kotlin always receives a JSON object. Field names are the Go
// json tags verbatim and are lowerCamelCase, unlike the struct DTOs in Models.kt
// which use Go field names.

@Serializable
private data class IdPayload(val id: String = "")

@Serializable
private data class PeerPayload(val peer: String = "")

@Serializable
private data class ProfileSetPayload(val user: User = User(), val device: Device = Device())

@Serializable
private data class SyncAcceptedPayload(
    val user: User = User(),
    val devices: List<Device> = emptyList(),
    val references: Boolean = false,
)

@Serializable
private data class DeviceNamePayload(val id: String = "", val name: String = "")

@Serializable
private data class DeviceLastSeenPayload(val id: String = "", val lastSeen: Long = 0)

@Serializable
private data class DMStatePayload(val id: String = "", val state: DMState = DMState())

@Serializable
private data class MessageDeliveredPayload(val messageId: String = "", val userId: String = "")

@Serializable
private data class CatchUpPayload(
    val update: BulkUpdate = BulkUpdate(),
    val initialSync: Boolean = false,
)
