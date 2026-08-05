package chat.bounce.data

import chat.bounce.goengine.Goengine

/**
 * A conversation as the thread list renders it.
 *
 * Groups and DMs are two unrelated engine types (chat.Group keyed by group UUID,
 * chat.User keyed by user UUID) that the list has to interleave and sort
 * together, so they are flattened into one row type here. [isGroup] is the only
 * thing that distinguishes them, and [id] is the thread ID either way - the
 * group UUID, or the other participant's user UUID.
 *
 * Timestamps are unix seconds.
 */
data class ThreadSummary(
    val id: String,
    val name: String,
    val isGroup: Boolean,
    /**
     * Newest avatar file UUID, or null to draw the generated fallback. Feed it
     * to [ImageCache] / [avatarBitmap]; the file may not have downloaded yet, in
     * which case the fallback is drawn anyway.
     */
    val avatarFileId: String?,
    val lastActivity: Long,
    /**
     * Text of the newest message, or the newest attachment's filename when that
     * message has no text. Empty when the newest item is a system event instead;
     * that text needs the user directory and localized strings, so the row builds
     * it from [lastSystemEvent] itself.
     */
    val lastMessagePreview: String,
    /**
     * Set when the newest thing in the thread is a status change rather than a
     * message, in which case [lastMessagePreview] is empty and the row renders
     * this instead. Null whenever a message is newest.
     */
    val lastSystemEvent: ConversationItem.SystemEvent? = null,
    val unreadCount: Int,
    /**
     * Delivery state of the newest message, but only when that message is ours -
     * null otherwise, including for a thread whose newest message came from the
     * other side.
     *
     * Deliberately tied to the same message [lastMessagePreview] describes, so
     * the glyph in the row can never be reporting on something other than the
     * line of text next to it.
     */
    val outgoingStatus: DeliveryState?,
    val mutedUntil: Long,
    val isPendingInvite: Boolean,
    val hasDraft: Boolean,
    val online: Boolean,
    /**
     * A DM to yourself. The engine treats it as an ordinary thread whose id is
     * your own user id, but it needs a distinct affordance in the UI - it has no
     * presence, and "you" is not a contact.
     */
    val isSelf: Boolean = false,
) {

    /**
     * The engine stores "muted" as a deadline, with -1 for "never unmute", so a
     * plain `mutedUntil != 0` check would report a long-expired mute as active.
     */
    val isMuted: Boolean
        get() = mutedUntil == MUTED_FOREVER || mutedUntil > System.currentTimeMillis() / 1000L

    companion object {
        /** chat.MutedForever (-1): muted with no expiry. */
        val MUTED_FOREVER: Long = Goengine.MutedForever
    }
}
