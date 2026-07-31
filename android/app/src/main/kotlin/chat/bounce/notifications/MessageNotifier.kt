package chat.bounce.notifications

import android.annotation.SuppressLint
import android.app.Notification
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.net.Uri
import android.util.Log
import android.util.LruCache
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.Person
import androidx.core.app.RemoteInput
import androidx.core.content.LocusIdCompat
import chat.bounce.R
import chat.bounce.data.Avatars
import chat.bounce.data.ChatRepository
import chat.bounce.data.ConversationItem
import chat.bounce.data.MessageNotifier
import chat.bounce.data.ThreadSummary
import chat.bounce.engine.EngineClient
import chat.bounce.engine.EngineSignal
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.util.UUID

/**
 * Turns the engine's notification callbacks into `MessagingStyle` conversations.
 *
 * The engine's `postNotification(id, title, content, openThread, icon)` is too
 * thin to build one of these on its own: for a group message it concatenates the
 * sender's name into `content`, it carries no timestamp, and it never says
 * whether the thread is a group. None of that is recovered by parsing strings.
 *
 * Instead this leans on the ordering guarantee of the signal channel. The engine
 * emits `DisplayDirectMessage`/`DisplayGroupMessage` - the whole structured
 * message, author UUID and all - immediately before it calls `postNotification`,
 * and both travel the same ordered channel to the same consumer. By the time a
 * [EngineSignal.NotificationPost] is handled, [ChatRepository.messageById] can
 * already resolve the author, the text and the timestamp, and
 * [ChatRepository.threadFor] knows whether the thread is a group, what it is
 * called and who is in it. `postNotification` is then used for the only thing it
 * uniquely knows: the *decision* to notify, which encodes the engine's mute,
 * foreground and cross-device do-not-disturb rules.
 *
 * The raw `title`/`content` are still honoured, but only as a fallback for posts
 * with no structured message behind them - group add/invite, and the rare case
 * of a repository miss.
 */
class BounceMessageNotifier(
    context: Context,
    private val client: EngineClient,
    private val repo: ChatRepository,
) : MessageNotifier {

    private val appContext = context.applicationContext
    private val manager = NotificationManagerCompat.from(appContext)

    /**
     * Per-thread message history, keyed by message UUID inside each thread.
     *
     * MessagingStyle history is owned by the app, not accumulated by the system:
     * every `notify()` re-supplies the whole conversation. A map rather than a
     * list because [onNotificationClear] has to remove one *specific* message -
     * a read receipt from another sync device names the message UUID, and two
     * messages with identical text and timestamp are otherwise indistinguishable.
     */
    private val histories = mutableMapOf<String, LinkedHashMap<String, NotificationCompat.MessagingStyle.Message>>()

    /** Reverse index so a clear can find the thread a message UUID belongs to. */
    private val messageThreads = mutableMapOf<String, String>()

    /** Notification ids of posts that had no structured message behind them. */
    private val plainNotifications = LinkedHashMap<String, Int>()

    private val avatarCache = LruCache<String, Bitmap>(AVATAR_CACHE_ENTRIES)

    /**
     * Everything that mutates [histories] runs under this. Posts and clears
     * arrive on the single event-pump coroutine, but notification actions arrive
     * on the broadcast receiver's own scope.
     */
    private val mutex = Mutex()

    /**
     * Where work that cannot run inline goes. [onNotificationClear] is not a
     * suspending callback - the engine's signal stream must not be held up by a
     * redraw - but redrawing a conversation reads avatars off disk, so it is
     * handed to this instead. Process-scoped alongside the service that owns the
     * notifier.
     */
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    init {
        NotificationChannels.ensure(appContext)
        active = this
    }

    // --- MessageNotifier ----------------------------------------------------

    override suspend fun onNotificationPost(post: EngineSignal.NotificationPost) {
        if (!canPost()) return

        val thread = repo.threadFor(post.openThread)
        val message = repo.messageById(post.id)
        if (thread == null || message == null) {
            // Group add/invite: `id` is the group UUID and there is no message at
            // all, only the literal string "You have been added to a group".
            postPlain(post, thread)
            return
        }

        val sender = senderPerson(thread, message.authorId)
        if (sender == null) {
            // An author we cannot name would render as an anonymous bubble; the
            // engine's own body text is more informative than that.
            postPlain(post, thread)
            return
        }

        val entry = NotificationCompat.MessagingStyle.Message(
            bodyOf(message),
            timestampMillisOf(message),
            sender,
        )
        mutex.withLock { append(thread.id, post.id, entry, trackForClear = true) }
        notifyThread(thread, alert = true)
    }

    /**
     * Not suspending, by contract: a read receipt arriving from a sync device
     * must not stall the signal stream behind a bitmap decode. The work is
     * therefore handed to [scope], which serialises against posts through
     * [mutex] the same way an inline call would have.
     */
    override fun onNotificationClear(messageId: String) {
        scope.launch { clearMessage(messageId) }
    }

    private suspend fun clearMessage(messageId: String) {
        val threadId = mutex.withLock {
            plainNotifications.remove(messageId)?.let { manager.cancel(it) }
            messageThreads.remove(messageId)?.also { histories[it]?.remove(messageId) }
        } ?: return

        val remaining = mutex.withLock { histories[threadId]?.size ?: 0 }
        if (remaining == 0) {
            clearThread(threadId)
            return
        }

        // Still unread messages in this conversation: redraw it without the one
        // that was read, and do it silently - nothing new has arrived.
        repo.threadFor(threadId)?.let { notifyThread(it, alert = false) }
    }

    // --- notification actions ----------------------------------------------

    /**
     * Sends [text] to [threadId] on behalf of an inline reply, then folds it into
     * the conversation so the shade reflects it immediately.
     *
     * The engine will not do that for us: it suppresses notifications for the
     * local user's own messages, so nothing would ever call back in here.
     */
    suspend fun replyTo(threadId: String, text: String) {
        val thread = repo.threadFor(threadId)
        try {
            if (thread?.isGroup == true) {
                client.sendGroupMessage(threadId, text)
            } else {
                client.sendDirectMessage(threadId, text)
            }
        } catch (t: Throwable) {
            Log.w(TAG, "inline reply to $threadId failed", t)
            return
        }

        // sender == null is how MessagingStyle denotes the local user.
        val entry = NotificationCompat.MessagingStyle.Message(
            text,
            System.currentTimeMillis(),
            null as Person?,
        )
        mutex.withLock {
            append(threadId, "local:${UUID.randomUUID()}", entry, trackForClear = false)
        }
        thread?.let { notifyThread(it, alert = false) }
    }

    /**
     * Marks a conversation read from the notification shade and takes the
     * notification down.
     *
     * The dismissal happens *first*, and that ordering is the point. Cancelling a
     * notification is a local, instant call; marking read is a blocking hop into
     * Go that has been measured on a real device at nearly twelve seconds. With
     * the engine call first, the notification sat on screen for the whole of it -
     * and if the call outran the broadcast budget the dismissal was skipped
     * altogether, because the timeout cancels this coroutine before it gets here.
     * Tapping the button now always takes the notification away, whatever the
     * engine does afterwards.
     */
    suspend fun markRead(threadId: String) {
        clearThread(threadId)

        // Clears the badge on the thread button. The engine call below marks the
        // same messages seen in the database, but it reports nothing back that
        // would reach the UI - the read state the thread list renders comes from
        // the messages this repository is already holding, so without this the
        // counter sits there unchanged until the next full reload. This is the
        // same call ConversationViewModel makes when a thread is opened.
        repo.markThreadRead(threadId)

        // null means the repository has not loaded this thread yet, which is the
        // normal case when the action starts a cold process. The previous code
        // treated null as "not a group" and sent every group thread down the DM
        // path, where it silently matched nothing and marked nothing.
        val isGroup = repo.threadFor(threadId)?.isGroup

        try {
            // Both calls are scoped by id and select zero rows for the wrong kind
            // of thread, so asking for both when we cannot tell is a no-op on one
            // side rather than a guess that quietly does nothing on both.
            if (isGroup != false) client.markAllGroupMessagesAsRead(threadId)
            if (isGroup != true) client.markAllDirectMessagesAsRead(threadId)
        } catch (t: Throwable) {
            Log.w(TAG, "mark-as-read for $threadId failed", t)
        }
    }

    /** Cancels a conversation's notification and forgets its history. */
    suspend fun clearThread(threadId: String) {
        mutex.withLock { forget(threadId) }
        manager.cancel(notificationIdFor(threadId))
    }

    /**
     * The user swiped the conversation away. The system has already removed it,
     * so only the history is dropped - otherwise the next message would resurrect
     * everything the user just dismissed.
     */
    suspend fun onDismissed(threadId: String) {
        mutex.withLock { forget(threadId) }
    }

    // --- building -----------------------------------------------------------

    private suspend fun notifyThread(thread: ThreadSummary, alert: Boolean) {
        if (!canPost()) return

        val history = mutex.withLock { histories[thread.id]?.values?.toList() }.orEmpty()
        if (history.isEmpty()) return

        // Before notify(), never after: a notification naming a shortcut that
        // does not exist yet is demoted out of the Conversations section.
        val hasShortcut = Conversations.pushConversationShortcut(appContext, client, repo, thread)

        // Catch-up replays history out of order, so sort rather than trust
        // insertion order.
        val ordered = history.sortedBy { it.timestamp }

        val style = NotificationCompat.MessagingStyle(localPerson())
        if (thread.isGroup) {
            // Only for groups. A conversation title on a 1:1 makes Android render
            // it as a group, complete with the sender's name above every line.
            style.setConversationTitle(thread.name)
        }
        style.setGroupConversation(thread.isGroup)
        ordered.forEach { style.addMessage(it) }

        val builder = baseBuilder(thread.id)
            .setStyle(style)
            .setWhen(ordered.last().timestamp)
            .setShowWhen(true)
            .setNumber(ordered.size)
            .setSilent(!alert)
            .addAction(replyAction(thread.id))
            .addAction(markReadAction(thread.id))

        avatar(thread.id, thread.name, listOfNotNull(thread.avatarFileId))
            ?.let { builder.setLargeIcon(it) }
        if (hasShortcut) builder.setShortcutId(thread.id)

        // No BubbleMetadata. A bubble additionally requires its target activity
        // to declare android:resizeableActivity="true", android:allowEmbedded="true"
        // and (in practice) android:documentLaunchMode="always"; MainActivity
        // declares none of those, and the platform silently drops bubbles from
        // non-conforming activities. Setting it here would ship a feature that
        // never appears. Adding those three attributes - or a second, lightweight
        // conversation activity - is all that is missing.

        notify(notificationIdFor(thread.id), builder.build())
    }

    /**
     * The fallback path: a post with no structured message behind it. Used for
     * group add/invite (where `id` is the group UUID and `content` is a literal
     * string) and for the rare post whose message the repository has not got.
     */
    private suspend fun postPlain(post: EngineSignal.NotificationPost, thread: ThreadSummary?) {
        val hasShortcut = thread != null &&
            Conversations.pushConversationShortcut(appContext, client, repo, thread)

        val builder = baseBuilder(post.openThread)
            .setContentTitle(post.title)
            .setContentText(post.content)
            .setStyle(NotificationCompat.BigTextStyle().bigText(post.content))
            .setWhen(System.currentTimeMillis())
            .setShowWhen(true)

        if (thread != null) {
            avatar(thread.id, thread.name, listOfNotNull(thread.avatarFileId))
                ?.let { builder.setLargeIcon(it) }
        }
        if (hasShortcut) builder.setShortcutId(post.openThread)

        // Keyed off the post id rather than the thread so an invite is not
        // clobbered by the first message in the group it invited you to. Group
        // invites are never passed to clearNotification by the engine, so this is
        // also the only notification the user has to dismiss by hand.
        val id = plainNotificationIdFor(post.id)
        mutex.withLock {
            plainNotifications[post.id] = id
            while (plainNotifications.size > MAX_PLAIN_NOTIFICATIONS) {
                plainNotifications.remove(plainNotifications.keys.first())
            }
        }
        notify(id, builder.build())
    }

    /**
     * Deliberately no `setGroup()`: from API 30 the system bundles conversation
     * notifications into the Conversations section itself, and below that it
     * auto-bundles once four are outstanding. The old client grouped by display
     * *name*, which merged two contacts who shared one and split a group the
     * moment it was renamed.
     */
    private fun baseBuilder(threadId: String): NotificationCompat.Builder =
        NotificationCompat.Builder(appContext, NotificationChannels.channelIdFor(threadId))
            .setSmallIcon(R.drawable.ic_notification)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .setContentIntent(openThreadPendingIntent(threadId))
            .setDeleteIntent(actionPendingIntent(NotificationActionReceiver.ACTION_DISMISS, threadId))
            .setLocusId(LocusIdCompat(threadId))

    // --- people -------------------------------------------------------------

    private suspend fun localPerson(): Person {
        val profile = repo.profile.value
        val id = profile?.id ?: LOCAL_PERSON_KEY
        val name = profile?.displayName ?: appContext.getString(R.string.notification_you)
        return person(id, name, profile?.images.orEmpty())
    }

    /**
     * The [Person] that sent [authorId] in [thread], or null when the author
     * cannot be named - in which case the caller should fall back to the plain
     * notification rather than attribute the message to nobody.
     */
    private suspend fun senderPerson(thread: ThreadSummary, authorId: String): Person? {
        participantsOf(repo, thread).firstOrNull { it.id == authorId }?.let {
            return person(it.id, it.displayName, it.images)
        }
        // A DM thread's id *is* the peer's user id, so a peer the user directory
        // has not caught up with still resolves.
        if (!thread.isGroup && authorId == thread.id) {
            return person(thread.id, thread.name, listOfNotNull(thread.avatarFileId))
        }
        return null
    }

    private suspend fun person(id: String, name: String, imageIds: List<String>): Person =
        Person.Builder()
            // Stable across renames and process restarts: this is what the system
            // dedupes, ranks and matches conversations on.
            .setKey(id)
            .setName(name)
            .setIcon(avatar(id, name, imageIds)?.let { adaptiveIconOf(it) })
            .build()

    /**
     * Avatars are re-rendered for every participant of every notification, so
     * they are cached on the identity that produced them - a rename or a new
     * avatar file changes the key and re-renders.
     *
     * The engine's own `icon` byte array is ignored: nothing in this client calls
     * `SetNotificationIcon`, and the PNG that callback was designed around is an
     * opaque square with the desktop theme's background baked into the corners,
     * which looks wrong under an adaptive icon's circular mask.
     */
    private suspend fun avatar(id: String, name: String, imageIds: List<String>): Bitmap? {
        val key = "$id|$name|${imageIds.joinToString(",")}"
        avatarCache.get(key)?.let { return it }
        val bitmap = runCatching {
            Avatars.avatarBitmap(client, imageIds, id, name, Conversations.AVATAR_PX)
        }.onFailure { Log.w(TAG, "could not render avatar for $id", it) }.getOrNull() ?: return null
        avatarCache.put(key, bitmap)
        return bitmap
    }

    // --- actions ------------------------------------------------------------

    private fun replyAction(threadId: String): NotificationCompat.Action {
        val remoteInput = RemoteInput.Builder(NotificationActionReceiver.KEY_REPLY_TEXT)
            .setLabel(appContext.getString(R.string.notification_reply_hint))
            .build()

        return NotificationCompat.Action.Builder(
            // Not rendered for an inline reply on any current Android version,
            // but the builder still requires a valid resource id.
            R.drawable.ic_notification,
            appContext.getString(R.string.notification_reply),
            actionPendingIntent(NotificationActionReceiver.ACTION_REPLY, threadId, mutable = true),
        )
            .addRemoteInput(remoteInput)
            .setSemanticAction(NotificationCompat.Action.SEMANTIC_ACTION_REPLY)
            .setAllowGeneratedReplies(true)
            .setShowsUserInterface(false)
            .build()
    }

    private fun markReadAction(threadId: String): NotificationCompat.Action =
        NotificationCompat.Action.Builder(
            R.drawable.ic_notification,
            appContext.getString(R.string.notification_mark_read),
            actionPendingIntent(NotificationActionReceiver.ACTION_MARK_READ, threadId),
        )
            .setSemanticAction(NotificationCompat.Action.SEMANTIC_ACTION_MARK_AS_READ)
            .setShowsUserInterface(false)
            .build()

    private fun openThreadPendingIntent(threadId: String): PendingIntent =
        PendingIntent.getActivity(
            appContext,
            notificationIdFor(threadId),
            Conversations.openThreadIntent(appContext, threadId),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )

    private fun actionPendingIntent(
        action: String,
        threadId: String,
        mutable: Boolean = false,
    ): PendingIntent {
        val intent = Intent(appContext, NotificationActionReceiver::class.java)
            .setAction(action)
            // PendingIntent identity ignores extras, so two threads sharing an
            // action would otherwise share (and overwrite) one PendingIntent, and
            // a reply meant for one conversation would be delivered to another.
            // The data URI is part of that identity; the request code is belt and
            // braces for hash collisions.
            .setData(Uri.parse("bounce://thread/$threadId/$action"))
            .putExtra(Conversations.EXTRA_THREAD_ID, threadId)

        // FLAG_MUTABLE is mandatory from API 31 for any PendingIntent the system
        // has to write RemoteInput results into. It is ignored on older releases.
        val mutability =
            if (mutable) PendingIntent.FLAG_MUTABLE else PendingIntent.FLAG_IMMUTABLE

        return PendingIntent.getBroadcast(
            appContext,
            notificationIdFor(threadId),
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or mutability,
        )
    }

    // --- state --------------------------------------------------------------

    private fun append(
        threadId: String,
        key: String,
        entry: NotificationCompat.MessagingStyle.Message,
        trackForClear: Boolean,
    ) {
        val history = histories.getOrPut(threadId) { LinkedHashMap() }
        history[key] = entry
        if (trackForClear) messageThreads[key] = threadId

        // Android retains 25 messages per conversation and renders about seven of
        // them; anything older is dead weight in the binder payload.
        while (history.size > MAX_HISTORY) {
            val oldest = history.keys.first()
            history.remove(oldest)
            messageThreads.remove(oldest)
        }
    }

    private fun forget(threadId: String) {
        histories.remove(threadId)?.keys?.forEach { messageThreads.remove(it) }
    }

    private fun bodyOf(message: ConversationItem.Message): String {
        if (message.text.isNotBlank()) return message.text
        val images = message.imageAttachments.size
        val files = message.fileAttachments.size
        return when {
            images > 0 && files > 0 -> appContext.getString(R.string.notification_attachment_mixed)
            images == 1 -> appContext.getString(R.string.notification_attachment_photo)
            images > 1 -> appContext.getString(R.string.notification_attachment_photos, images)
            files == 1 -> appContext.getString(R.string.notification_attachment_file)
            files > 1 -> appContext.getString(R.string.notification_attachment_files, files)
            else -> ""
        }
    }

    /** Engine timestamps are epoch *seconds*; MessagingStyle wants millis. */
    private fun timestampMillisOf(message: ConversationItem.Message): Long =
        if (message.writtenAt > 0) message.writtenAt * 1000L else System.currentTimeMillis()

    private fun canPost(): Boolean = manager.areNotificationsEnabled()

    @SuppressLint("MissingPermission") // guarded by canPost() / areNotificationsEnabled()
    private fun notify(id: Int, notification: Notification) {
        if (!canPost()) return
        runCatching { manager.notify(id, notification) }
            .onFailure { Log.w(TAG, "notify($id) failed", it) }
    }

    companion object {
        /**
         * Stable per *conversation*, never per message - that is what makes
         * notify() update the conversation in place instead of stacking a fresh
         * notification per message. The old Java client allocated an incrementing
         * int per notification, which both stacked and, because the counter reset
         * with the process, silently replaced unrelated notifications after a
         * restart.
         */
        fun notificationIdFor(threadId: String): Int = threadId.hashCode()

        private fun plainNotificationIdFor(postId: String): Int = "plain:$postId".hashCode()

        /**
         * The broadcast receiver runs in this same process but is constructed by
         * the system, so it cannot be handed the notifier. It is set once, by the
         * single application-scoped instance the service creates.
         */
        @Volatile
        private var active: BounceMessageNotifier? = null

        val current: BounceMessageNotifier? get() = active

        private const val TAG = "BounceNotifier"
        private const val MAX_HISTORY = 25
        private const val MAX_PLAIN_NOTIFICATIONS = 32
        private const val AVATAR_CACHE_ENTRIES = 24

        /** Used only before the profile has loaded; replaced on the next post. */
        private const val LOCAL_PERSON_KEY = "chat.bounce.local"
    }
}
