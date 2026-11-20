package chat.bounce.engine

/**
 * Everything the Go engine pushes at us, in one ordered stream.
 *
 * UI events and notification requests deliberately share a channel. The engine
 * emits chat.UI.DisplayDirectMessage *before* it calls postNotification for the
 * same message, so a single ordered consumer is guaranteed to have already
 * recorded the structured message - author, timestamp, attachments - by the
 * time it handles the matching [NotificationPost].
 *
 * That is what makes a proper MessagingStyle notification possible without
 * touching the Go engine: postNotification alone carries only
 * (id, title, content, openThread, icon), which for a group message has the
 * sender name string-concatenated into the body and no timestamp at all. The
 * event stream supplies the structure; the notification callback supplies the
 * *decision* to notify, which encodes the engine's mute, foreground and
 * cross-device DND rules that should not be reimplemented here.
 */
sealed interface EngineSignal {

    /** A structured chat.UI callback. [kind] is the Go method name. */
    data class Raw(val kind: String, val payload: String) : EngineSignal

    data class Typing(val userId: String, val threadId: String, val typing: Boolean) : EngineSignal

    data class Presence(val id: String, val kind: String, val online: Boolean) : EngineSignal {
        val isUser: Boolean get() = kind == "user"
    }

    data class Progress(val id: String, val kind: String, val fraction: Double) : EngineSignal {
        val isInitialSync: Boolean get() = kind == "initialsync"
    }

    /**
     * The engine decided this message warrants a notification.
     *
     * [id] is the *message* UUID (or, for group add/invite, the group UUID),
     * [openThread] is the conversation UUID. [icon] is a 128x128 PNG of the
     * conversation avatar, possibly empty.
     */
    data class NotificationPost(
        val id: String,
        val title: String,
        val content: String,
        val openThread: String,
        val icon: ByteArray?,
    ) : EngineSignal {
        // ByteArray breaks data-class equality; these are never compared, but
        // overriding keeps the compiler warning honest.
        override fun equals(other: Any?): Boolean = this === other
        override fun hashCode(): Int = id.hashCode()
    }

    /** Dismiss the notification for a message UUID; the user read it elsewhere. */
    data class NotificationClear(val id: String) : EngineSignal
}
