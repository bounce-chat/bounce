package chat.bounce.engine

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Kotlin mirrors of the DTOs in chat/ui.go.
 *
 * The engine marshals those structs with encoding/json and no struct tags, so
 * every key is the Go field name verbatim - `ID`, `WrittenAt`, `ImageAttachments`.
 * Rather than fight that from the Go side (which would mean either tagging the
 * shared chat package or maintaining a second set of mirror structs that can
 * drift), the naming is pinned here with @SerialName.
 *
 * UUIDs arrive as canonical hyphenated strings because uuid.UUID implements
 * TextMarshaler; they are kept as String throughout rather than parsed into
 * java.util.UUID, since every one of them is used as a map key or passed
 * straight back to the engine as a string.
 */

@Serializable
data class DMState(
    @SerialName("Open") val open: Boolean = false,
    @SerialName("Retention") val retention: Long = 0,
    @SerialName("MutedUntil") val mutedUntil: Long = 0,
    @SerialName("LastActivity") val lastActivity: Long = 0,
    @SerialName("OverrideReadReceiptSetting") val overrideReadReceiptSetting: Boolean = false,
    @SerialName("ReadReceiptsEnabled") val readReceiptsEnabled: Boolean = false,
    @SerialName("OverrideTypingIndicatorSetting") val overrideTypingIndicatorSetting: Boolean = false,
    @SerialName("TypingIndicatorsEnabled") val typingIndicatorsEnabled: Boolean = false,
    @SerialName("LastOpened") val lastOpened: Long = 0,
)

@Serializable
data class User(
    @SerialName("ID") val id: String = "",
    @SerialName("Name") val name: String = "",
    @SerialName("Images") val images: List<String> = emptyList(),
    @SerialName("State") val state: DMState = DMState(),
    @SerialName("IntroductionTime") val introductionTime: Long = 0,
    @SerialName("Alias") val alias: String = "",
    @SerialName("Notes") val notes: String = "",
    @SerialName("Blocked") val blocked: Boolean = false,
) {
    /** What the UI should call this person: their chosen alias wins over the self-declared name. */
    val displayName: String get() = alias.ifBlank { name }.ifBlank { "Unknown" }
}

@Serializable
data class Device(
    @SerialName("ID") val id: String = "",
    @SerialName("Name") val name: String = "",
    @SerialName("Address") val address: String = "",
    @SerialName("LastSeen") val lastSeen: Long = 0,
    @SerialName("CreatedAt") val createdAt: Long = 0,
    @SerialName("Local") val local: Boolean = false,
    @SerialName("Encrypted") val encrypted: Boolean = false,
    @SerialName("Online") val online: Boolean = false,
)

@Serializable
data class Settings(
    @SerialName("DefaultGroupRetention") val defaultGroupRetention: Long = 0,
    @SerialName("DefaultSendReadReceipts") val defaultSendReadReceipts: Boolean = true,
    @SerialName("DefaultSendTypingIndicators") val defaultSendTypingIndicators: Boolean = true,
    @SerialName("NewGroupRestrictUserManagement") val newGroupRestrictUserManagement: Boolean = true,
    @SerialName("NewGroupRestrictGroupEdits") val newGroupRestrictGroupEdits: Boolean = false,
    @SerialName("NewGroupRestrictPosting") val newGroupRestrictPosting: Boolean = false,
    @SerialName("AutoJoinGroups") val autoJoinGroups: Int = 0,
    @SerialName("DefaultDMRetention") val defaultDMRetention: Long = 0,
)

@Serializable
data class ImageAttachment(
    @SerialName("ID") val id: String = "",
    @SerialName("Name") val name: String = "",
    @SerialName("Size") val size: Long = 0,
    @SerialName("Width") val width: Int = 0,
    @SerialName("Height") val height: Int = 0,
    @SerialName("BlurHash") val blurHash: String = "",
)

@Serializable
data class FileAttachment(
    @SerialName("ID") val id: String = "",
    @SerialName("Name") val name: String = "",
    @SerialName("Size") val size: Long = 0,
    @SerialName("Progress") val progress: Double = 0.0,
)

@Serializable
data class ReadReceipt(
    @SerialName("ID") val id: String = "",
    @SerialName("Actor") val actor: String = "",
    @SerialName("Target") val target: String = "",
)

@Serializable
data class DirectMessage(
    @SerialName("ID") val id: String = "",
    @SerialName("Author") val author: String = "",
    @SerialName("Thread") val thread: String = "",
    @SerialName("WrittenAt") val writtenAt: Long = 0,
    @SerialName("SavedAt") val savedAt: Long = 0,
    @SerialName("ExpiresAt") val expiresAt: Long = 0,
    @SerialName("Text") val text: String = "",
    @SerialName("Seen") val seen: Boolean = false,
    @SerialName("Undeliverable") val undeliverable: Boolean = false,
    @SerialName("ImageAttachments") val imageAttachments: List<ImageAttachment> = emptyList(),
    @SerialName("FileAttachments") val fileAttachments: List<FileAttachment> = emptyList(),
    @SerialName("ReadReceipts") val readReceipts: List<ReadReceipt> = emptyList(),
    @SerialName("DeliveredTo") val deliveredTo: List<String> = emptyList(),
)

@Serializable
data class GroupMessage(
    @SerialName("ID") val id: String = "",
    @SerialName("Author") val author: String = "",
    @SerialName("Thread") val thread: String = "",
    @SerialName("WrittenAt") val writtenAt: Long = 0,
    @SerialName("SavedAt") val savedAt: Long = 0,
    @SerialName("ExpiresAt") val expiresAt: Long = 0,
    @SerialName("Text") val text: String = "",
    @SerialName("Seen") val seen: Boolean = false,
    @SerialName("Undeliverable") val undeliverable: Boolean = false,
    @SerialName("ImageAttachments") val imageAttachments: List<ImageAttachment> = emptyList(),
    @SerialName("FileAttachments") val fileAttachments: List<FileAttachment> = emptyList(),
    @SerialName("ReadReceipts") val readReceipts: List<ReadReceipt> = emptyList(),
    @SerialName("DeliveredTo") val deliveredTo: List<String> = emptyList(),
)

@Serializable
data class Group(
    @SerialName("ID") val id: String = "",
    @SerialName("Name") val name: String = "",
    @SerialName("Images") val images: List<String> = emptyList(),
    @SerialName("Users") val users: List<User> = emptyList(),
    @SerialName("Admins") val admins: List<String> = emptyList(),
    @SerialName("Invites") val invites: List<User> = emptyList(),
    @SerialName("InvitedBy") val invitedBy: String = "",
    @SerialName("InvitedAt") val invitedAt: Long = 0,
    @SerialName("AcceptedAt") val acceptedAt: Long = 0,
    @SerialName("BlockedUsers") val blockedUsers: List<String> = emptyList(),
    @SerialName("Retention") val retention: Long = 0,
    @SerialName("MutedUntil") val mutedUntil: Long = 0,
    @SerialName("LastActivity") val lastActivity: Long = 0,
    @SerialName("CreatedBy") val createdBy: String = "",
    @SerialName("CreatedAt") val createdAt: Long = 0,
    @SerialName("RestrictUserManagement") val restrictUserManagement: Boolean = false,
    @SerialName("RestrictGroupEdits") val restrictGroupEdits: Boolean = false,
    @SerialName("RestrictPosting") val restrictPosting: Boolean = false,
    @SerialName("OverrideReadReceiptSetting") val overrideReadReceiptSetting: Boolean = false,
    @SerialName("ReadReceiptsEnabled") val readReceiptsEnabled: Boolean = false,
    @SerialName("OverrideTypingIndicatorSetting") val overrideTypingIndicatorSetting: Boolean = false,
    @SerialName("TypingIndicatorsEnabled") val typingIndicatorsEnabled: Boolean = false,
    @SerialName("LastOpened") val lastOpened: Long = 0,
) {
    /** An invite is pending until it is accepted; acceptedAt is 0 until then. */
    val isPendingInvite: Boolean get() = invitedAt != 0L && acceptedAt == 0L
}

/**
 * Sent to Engine.createGroup as JSON. Image is deliberately absent: the avatar
 * crosses as a separate byte[] so it never passes through a JSON string.
 */
@Serializable
data class NewGroup(
    @SerialName("Name") val name: String,
    @SerialName("InitialInvites") val initialInvites: List<String> = emptyList(),
    @SerialName("Retention") val retention: Long = 0,
    @SerialName("RestrictUserManagement") val restrictUserManagement: Boolean = false,
    @SerialName("RestrictGroupEdits") val restrictGroupEdits: Boolean = false,
    @SerialName("RestrictPosting") val restrictPosting: Boolean = false,
)

@Serializable
data class Draft(
    @SerialName("Thread") val thread: String = "",
    @SerialName("Text") val text: String = "",
)

@Serializable
data class FileProgress(
    @SerialName("ID") val id: String = "",
    @SerialName("Progress") val progress: Double = 0.0,
)

// --- thread system/status items ---------------------------------------------
// These render as centred grey rows in a conversation rather than as bubbles.

@Serializable
data class UpdateDMRetention(
    @SerialName("ID") val id: String = "",
    @SerialName("Thread") val thread: String = "",
    @SerialName("Actor") val actor: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
    @SerialName("Seen") val seen: Boolean = false,
    @SerialName("Retention") val retention: Long = 0,
)

@Serializable
data class UpdateDMClearHistory(
    @SerialName("ID") val id: String = "",
    @SerialName("Thread") val thread: String = "",
    @SerialName("Actor") val actor: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
    @SerialName("Seen") val seen: Boolean = false,
    @SerialName("ClearTime") val clearTime: Long = 0,
)

@Serializable
data class UpdateDMSetAlias(
    @SerialName("ID") val id: String = "",
    @SerialName("User") val user: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
    @SerialName("Alias") val alias: String = "",
)

@Serializable
data class UpdateGroupRetention(
    @SerialName("ID") val id: String = "",
    @SerialName("Thread") val thread: String = "",
    @SerialName("Actor") val actor: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
    @SerialName("Seen") val seen: Boolean = false,
    @SerialName("Retention") val retention: Long = 0,
)

@Serializable
data class UpdateGroupName(
    @SerialName("ID") val id: String = "",
    @SerialName("Thread") val thread: String = "",
    @SerialName("Actor") val actor: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
    @SerialName("Seen") val seen: Boolean = false,
    @SerialName("Name") val name: String = "",
)

@Serializable
data class UpdateGroupInviteUser(
    @SerialName("ID") val id: String = "",
    @SerialName("Thread") val thread: String = "",
    @SerialName("Actor") val actor: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
    @SerialName("Seen") val seen: Boolean = false,
    @SerialName("User") val user: User = User(),
)

@Serializable
data class UpdateGroupRemoveUser(
    @SerialName("ID") val id: String = "",
    @SerialName("Thread") val thread: String = "",
    @SerialName("Actor") val actor: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
    @SerialName("Seen") val seen: Boolean = false,
    @SerialName("User") val user: String = "",
)

@Serializable
data class UpdateGroupClearHistory(
    @SerialName("ID") val id: String = "",
    @SerialName("Thread") val thread: String = "",
    @SerialName("Actor") val actor: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
    @SerialName("Seen") val seen: Boolean = false,
    @SerialName("ClearTime") val clearTime: Long = 0,
)

/**
 * The group updates that carry no payload beyond who did it and when all share
 * this shape: admin promote/demote and the six restrict/unrestrict variants.
 * They are distinguished by the event kind, not by their contents.
 */
@Serializable
data class GroupActorUpdate(
    @SerialName("ID") val id: String = "",
    @SerialName("Thread") val thread: String = "",
    @SerialName("Actor") val actor: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
    @SerialName("Seen") val seen: Boolean = false,
    @SerialName("UserID") val userId: String = "",
)

@Serializable
data class RemovedFromGroup(
    @SerialName("Group") val group: String = "",
    @SerialName("Actor") val actor: String = "",
)

@Serializable
data class GroupDeleted(
    @SerialName("Group") val group: String = "",
    @SerialName("Actor") val actor: String = "",
)

@Serializable
data class UpdateUserUpdateName(
    @SerialName("ID") val id: String = "",
    @SerialName("User") val user: String = "",
    @SerialName("OldName") val oldName: String = "",
    @SerialName("Name") val name: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
)

@Serializable
data class UpdateUserUpdateImage(
    @SerialName("ID") val id: String = "",
    @SerialName("User") val user: String = "",
    @SerialName("Timestamp") val timestamp: Long = 0,
)

/**
 * The map values are nullable, and that is not defensiveness - it is required.
 *
 * Go marshals a nil slice as `null`, never `[]`, and catch_up.go builds these
 * with `m[id] = append(m[id], xs...)`, which leaves nil whenever `xs` is empty.
 * A message with no early read receipts - almost every message - therefore
 * emits `"<id>": null`.
 *
 * `coerceInputValues` does not help here: it substitutes defaults for
 * *properties*, and never reaches inside a collection's element type. Declaring
 * the value as non-null `List<T>` made the whole payload throw, taking every
 * message in the catch-up with it.
 */
@Serializable
data class BulkUpdate(
    @SerialName("Source") val source: String = "",
    @SerialName("Seen") val seen: List<String> = emptyList(),
    @SerialName("GroupMessages") val groupMessages: Map<String, List<GroupMessage>?> = emptyMap(),
    @SerialName("DirectMessages") val directMessages: Map<String, List<DirectMessage>?> = emptyMap(),
    @SerialName("ReadReceipts") val readReceipts: Map<String, List<ReadReceipt>?> = emptyMap(),
)

/**
 * The whole UI state, returned by Engine.getInitialState() and (scoped to one
 * thread) by Engine.getDMHistory().
 */
@Serializable
data class InitialState(
    @SerialName("DeviceRevoked") val deviceRevoked: Boolean = false,
    @SerialName("NetworkOnline") val networkOnline: Boolean = false,
    @SerialName("Profile") val profile: User? = null,
    @SerialName("Settings") val settings: Settings = Settings(),
    @SerialName("SyncDevices") val syncDevices: List<Device> = emptyList(),
    @SerialName("Users") val users: List<User> = emptyList(),
    @SerialName("Groups") val groups: List<Group> = emptyList(),
    @SerialName("DirectMessages") val directMessages: List<DirectMessage> = emptyList(),
    @SerialName("UpdateDMRetentions") val updateDMRetentions: List<UpdateDMRetention> = emptyList(),
    @SerialName("UpdateDMClearHistories") val updateDMClearHistories: List<UpdateDMClearHistory> = emptyList(),
    @SerialName("GroupMessages") val groupMessages: List<GroupMessage> = emptyList(),
    @SerialName("UpdateGroupRetentions") val updateGroupRetentions: List<UpdateGroupRetention> = emptyList(),
    @SerialName("UpdateGroupNames") val updateGroupNames: List<UpdateGroupName> = emptyList(),
    @SerialName("UpdateGroupInvitedUsers") val updateGroupInvitedUsers: List<UpdateGroupInviteUser> = emptyList(),
    @SerialName("UpdateGroupRemoveUsers") val updateGroupRemoveUsers: List<UpdateGroupRemoveUser> = emptyList(),
    @SerialName("UpdateGroupClearHistories") val updateGroupClearHistories: List<UpdateGroupClearHistory> = emptyList(),
    @SerialName("UpdateGroupAdminPromotions") val updateGroupAdminPromotions: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupAdminDemotions") val updateGroupAdminDemotions: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupUserManagementsRestricted") val userManagementRestricted: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupUserManagementsUnrestricted") val userManagementUnrestricted: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupEditsRestricted") val groupEditsRestricted: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupEditsUnrestricted") val groupEditsUnrestricted: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupPostingsRestricted") val postingRestricted: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupPostingsUnrestricted") val postingUnrestricted: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupUserBlockedGroups") val userBlockedGroups: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupUserChangedGroupImages") val userChangedGroupImages: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupRevokedInvites") val revokedInvites: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupAcceptedInvites") val acceptedInvites: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateGroupRejectedInvites") val rejectedInvites: List<GroupActorUpdate> = emptyList(),
    @SerialName("UpdateUserUpdateNames") val updateUserNames: List<UpdateUserUpdateName> = emptyList(),
    @SerialName("UpdateUserUpdateImages") val updateUserImages: List<UpdateUserUpdateImage> = emptyList(),
    @SerialName("FileProgress") val fileProgress: List<FileProgress> = emptyList(),
    @SerialName("Drafts") val drafts: List<Draft> = emptyList(),
)

/** One outgoing attachment, serialized for Engine.sendDirectMessage/sendGroupMessage. */
@Serializable
data class OutgoingAttachment(
    @SerialName("id") val id: String,
    @SerialName("path") val path: String,
    @SerialName("name") val name: String,
    @SerialName("size") val size: Long = 0,
)
