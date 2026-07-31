package chat.bounce.data

import chat.bounce.engine.DirectMessage
import chat.bounce.engine.FileAttachment
import chat.bounce.engine.GroupMessage
import chat.bounce.engine.ImageAttachment
import chat.bounce.engine.ReadReceipt

/**
 * One row in a conversation.
 *
 * The engine hands the UI two very different shapes - chat messages and the
 * ~20 "somebody changed something" update frames - through 25 separate
 * chat.UI callbacks. Everything the UI needs to render, sort and key a
 * conversation is normalized into this one hierarchy so the conversation
 * screen has a single list type and a single sort key.
 *
 * All timestamps are unix *seconds*, matching the engine.
 */
sealed interface ConversationItem {

    /** Stable across restarts; the engine's frame UUID. Unique within a thread. */
    val id: String

    /** For a group, the group UUID; for a DM, the other participant's user UUID. */
    val threadId: String

    val timestamp: Long

    data class Message(
        override val id: String,
        override val threadId: String,
        val authorId: String,
        val text: String,
        val writtenAt: Long,
        val seen: Boolean,
        val undeliverable: Boolean,
        val outgoing: Boolean,
        /**
         * Epoch seconds at which this message is deleted, or 0 for a message
         * that never expires. Set from the thread's retention at send time, so
         * it is a property of the message rather than of the thread as it
         * stands now - an old message keeps the retention it was sent under.
         */
        val expiresAt: Long = 0,
        val imageAttachments: List<ImageAttachment> = emptyList(),
        val fileAttachments: List<FileAttachment> = emptyList(),
        val readReceipts: List<ReadReceipt> = emptyList(),
        val deliveredTo: List<String> = emptyList(),
    ) : ConversationItem {
        override val timestamp: Long get() = writtenAt

        val hasAttachments: Boolean
            get() = imageAttachments.isNotEmpty() || fileAttachments.isNotEmpty()
    }

    /**
     * A centred grey row: someone renamed the group, changed retention, was
     * promoted, and so on.
     *
     * The fields are the *data* behind the sentence, not the sentence itself.
     * Rendering it needs the user directory (to turn [actorId] into a display
     * name, and to say "You" for the local profile) and localized strings, both
     * of which live above the data layer.
     */
    data class SystemEvent(
        override val id: String,
        override val threadId: String,
        override val timestamp: Long,
        val kind: SystemEventKind,
        val actorId: String,
        /**
         * Who the action was done to, when the kind has a target: the promoted
         * admin, the invited/removed user, the renamed user.
         */
        val targetUserId: String? = null,
        /**
         * The new value the action set, when the kind carries one: the group's
         * new name, a user's new name, the alias, or an invited user's
         * self-declared name.
         */
        val name: String = "",
        /** The value [name] replaced. Only [SystemEventKind.USER_RENAMED] sets it. */
        val previousName: String = "",
        /** Seconds; only [SystemEventKind.RETENTION_CHANGED] sets it. 0 means "forever". */
        val retention: Long = 0,
        val seen: Boolean = true,
    ) : ConversationItem
}

/**
 * Which sentence a [ConversationItem.SystemEvent] represents.
 *
 * USER_REMOVED covers both "removed X" and "left" - the engine emits the same
 * frame for both and distinguishes them by `actor == target`, so the renderer
 * makes that check rather than the data layer inventing a second kind.
 */
enum class SystemEventKind {
    RETENTION_CHANGED,
    HISTORY_CLEARED,
    GROUP_RENAMED,
    USER_INVITED,
    USER_REMOVED,
    INVITE_REVOKED,
    INVITE_ACCEPTED,
    INVITE_REJECTED,
    ADMIN_PROMOTED,
    ADMIN_DEMOTED,
    USER_MANAGEMENT_RESTRICTED,
    USER_MANAGEMENT_UNRESTRICTED,
    GROUP_EDITS_RESTRICTED,
    GROUP_EDITS_UNRESTRICTED,
    POSTING_RESTRICTED,
    POSTING_UNRESTRICTED,
    USER_BLOCKED_GROUP,
    GROUP_IMAGE_CHANGED,
    USER_RENAMED,
    USER_IMAGE_CHANGED,
    ALIAS_SET,
}

/**
 * DirectMessage.Thread is `xor(dm.Xor, myID)` on the Go side - always the other
 * participant - so it is the DM thread ID unmodified, for sent and received
 * messages alike.
 */
fun DirectMessage.toConversationItem(myUserId: String): ConversationItem.Message =
    ConversationItem.Message(
        id = id,
        threadId = thread,
        authorId = author,
        text = text,
        writtenAt = writtenAt,
        seen = seen,
        undeliverable = undeliverable,
        outgoing = author == myUserId,
        expiresAt = expiresAt,
        imageAttachments = imageAttachments,
        fileAttachments = fileAttachments,
        readReceipts = readReceipts,
        deliveredTo = deliveredTo,
    )

fun GroupMessage.toConversationItem(myUserId: String): ConversationItem.Message =
    ConversationItem.Message(
        id = id,
        threadId = thread,
        authorId = author,
        text = text,
        writtenAt = writtenAt,
        seen = seen,
        undeliverable = undeliverable,
        outgoing = author == myUserId,
        expiresAt = expiresAt,
        imageAttachments = imageAttachments,
        fileAttachments = fileAttachments,
        readReceipts = readReceipts,
        deliveredTo = deliveredTo,
    )
