package chat.bounce.notifications

import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Canvas
import android.util.Log
import androidx.core.app.Person
import androidx.core.content.LocusIdCompat
import androidx.core.content.pm.ShortcutInfoCompat
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.core.graphics.drawable.IconCompat
import chat.bounce.data.Avatars
import chat.bounce.data.ChatRepository
import chat.bounce.data.ThreadSummary
import chat.bounce.engine.EngineClient
import chat.bounce.engine.User
import chat.bounce.ui.MainActivity
import java.util.concurrent.ConcurrentHashMap
import kotlin.math.max
import kotlin.math.roundToInt

/**
 * Conversation shortcuts - the thing that turns an ordinary notification into a
 * *conversation* on Android 11+.
 *
 * A notification that names a `shortcutId` with no matching long-lived shortcut
 * is not merely un-decorated: the system logs "Notification associated with
 * shortcut ... that doesn't exist" and demotes it out of the Conversations
 * section entirely. So the shortcut is always published first and
 * [pushConversationShortcut] reports whether it is safe for the caller to set
 * `setShortcutId`.
 *
 * The shortcut id is the raw thread UUID, which is also the locus id and the
 * `thread_id` extra the activity deep-links on - one identifier, no mapping
 * table to keep in sync.
 */
object Conversations {

    /** Extra on the MainActivity intent naming the conversation to open. */
    const val EXTRA_THREAD_ID = "thread_id"

    /** Square edge, in px, of every avatar rendered into a notification. */
    const val AVATAR_PX = 128

    /**
     * `android.content.pm.ShortcutInfo.SHORTCUT_CATEGORY_CONVERSATION`, spelled
     * out. The platform constant is API 30 and androidx.core (1.19.0) ships no
     * compat mirror of it; the value is only a category string, inert on 29.
     */
    private const val CATEGORY_CONVERSATION = "android.shortcut.conversation"

    private const val TAG = "BounceConversations"

    /**
     * Launchers advertise anywhere from 5 to 15 slots. Publishing to the top of
     * that range means rendering fifteen avatars at startup for shortcuts nobody
     * will see, so the useful range is clamped; the lower bound guards against a
     * launcher reporting 0.
     */
    private const val MIN_CONVERSATIONS = 1
    private const val MAX_CONVERSATIONS = 8

    /**
     * The system only surfaces a couple of faces per conversation, and every
     * extra Person means another avatar render plus another icon in the binder
     * payload.
     */
    private const val MAX_PERSONS = 4

    /**
     * Last-published signature per thread. Re-pushing an identical shortcut on
     * every incoming message is pure IPC, and it also churns the launcher's
     * dynamic-shortcut LRU for no reason.
     */
    private val published = ConcurrentHashMap<String, String>()

    /**
     * Publishes (or refreshes) the long-lived conversation shortcut for [thread].
     *
     * @return true when a shortcut for this thread is live, i.e. when the caller
     *   may set `setShortcutId(thread.id)` on a notification.
     */
    suspend fun pushConversationShortcut(
        context: Context,
        client: EngineClient,
        repo: ChatRepository,
        thread: ThreadSummary,
        rank: Int = 0,
        force: Boolean = false,
    ): Boolean {
        // ShortcutInfoCompat.Builder.build() throws on an empty label. A thread
        // with no name is not worth crashing the notification path over - it just
        // does not get a shortcut, and the notification posts without one.
        if (thread.name.isBlank()) return false

        val participants = participantsOf(repo, thread)

        val signature = signatureOf(thread, participants, rank)
        if (!force && published[thread.id] == signature) return true

        val appContext = context.applicationContext
        val localId = repo.profile.value?.id

        val persons = participants
            .asSequence()
            .filter { it.id != localId }
            .take(MAX_PERSONS)
            .toList()
            .map { user ->
                Person.Builder()
                    .setKey(user.id)
                    .setName(user.displayName)
                    .setIcon(avatarIcon(client, user.id, user.displayName, user.images))
                    .build()
            }

        val icon = avatarIcon(client, thread.id, thread.name, listOfNotNull(thread.avatarFileId))

        return runCatching {
            val shortcut = ShortcutInfoCompat.Builder(appContext, thread.id)
                .setShortLabel(thread.name)
                .setLongLabel(thread.name)
                .setIcon(icon)
                .setIntent(openThreadIntent(appContext, thread.id))
                .setLongLived(true)
                .setRank(rank)
                .setCategories(setOf(CATEGORY_CONVERSATION))
                .setLocusId(LocusIdCompat(thread.id))
                .apply { if (persons.isNotEmpty()) setPersons(persons.toTypedArray()) }
                .build()
            ShortcutManagerCompat.pushDynamicShortcut(appContext, shortcut)
        }
            .onFailure { Log.w(TAG, "could not publish shortcut for ${thread.id}", it) }
            .getOrDefault(false)
            .also { ok -> if (ok) published[thread.id] = signature else published.remove(thread.id) }
    }

    /**
     * Seeds the launcher with the most recently active conversations so that the
     * first notification of the session already lands in the Conversations
     * section, and so the launcher long-press menu is useful before any message
     * arrives. Call once after initial state has loaded.
     */
    suspend fun refreshTopConversations(
        context: Context,
        client: EngineClient,
        repo: ChatRepository,
    ) {
        val slots = ShortcutManagerCompat.getMaxShortcutCountPerActivity(context)
            .coerceIn(MIN_CONVERSATIONS, MAX_CONVERSATIONS)

        repo.threads.value
            .sortedByDescending { it.lastActivity }
            .take(slots)
            .forEachIndexed { index, thread ->
                pushConversationShortcut(context, client, repo, thread, rank = index, force = true)
            }
    }

    /** Drops the shortcut for a thread the user left, blocked or deleted. */
    fun removeConversationShortcut(context: Context, threadId: String) {
        published.remove(threadId)
        val ids = listOf(threadId)
        runCatching {
            ShortcutManagerCompat.removeDynamicShortcuts(context.applicationContext, ids)
            // A shortcut cached by the system because a notification referenced it
            // survives removeDynamicShortcuts; this is what actually retires it.
            ShortcutManagerCompat.removeLongLivedShortcuts(context.applicationContext, ids)
        }.onFailure { Log.w(TAG, "could not remove shortcut for $threadId", it) }
    }

    /** The deep link a tap resolves to, shared by shortcuts and notifications. */
    fun openThreadIntent(context: Context, threadId: String): Intent =
        Intent(context.applicationContext, MainActivity::class.java)
            // A shortcut intent without an action is rejected outright.
            .setAction(Intent.ACTION_VIEW)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
            .putExtra(EXTRA_THREAD_ID, threadId)

    private fun signatureOf(
        thread: ThreadSummary,
        participants: List<User>,
        rank: Int,
    ): String = buildString {
        append(thread.name).append(' ')
        append(thread.isGroup).append(' ')
        append(rank).append(' ')
        append(thread.avatarFileId).append(' ')
        participants.forEach { append(it.id).append(':').append(it.displayName).append(',') }
    }

    /**
     * Renders an avatar and wraps it for the adaptive-icon pipeline, falling back
     * to no icon rather than dropping the shortcut/person if rendering fails.
     */
    internal suspend fun avatarIcon(
        client: EngineClient,
        id: String,
        name: String,
        imageIds: List<String>,
    ): IconCompat? = runCatching {
        adaptiveIconOf(Avatars.avatarBitmap(client, imageIds, id, name, AVATAR_PX))
    }.onFailure { Log.w(TAG, "could not render avatar for $id", it) }.getOrNull()
}

/**
 * Who is in a conversation.
 *
 * [ThreadSummary] is a flattened row and carries no member list: a group's comes
 * from its engine record, and a DM has exactly one other participant - the
 * contact whose user UUID *is* the thread id.
 */
internal fun participantsOf(repo: ChatRepository, thread: ThreadSummary): List<User> =
    if (thread.isGroup) repo.groupById(thread.id)?.users.orEmpty()
    else listOfNotNull(repo.userById(thread.id))

/**
 * Wraps a bitmap as an adaptive icon, padded so it survives the mask.
 *
 * `AdaptiveIconDrawable` lays its layers out at 1 + 2 * `getExtraInsetFraction()`
 * (= 1.5x) of the view bounds and then clips to the launcher's mask, so the
 * outer 25% on every side of the source bitmap is cropped away. A circular
 * avatar handed over unpadded therefore loses its rim. Padding the source by the
 * same 25% first makes the visible result exactly the original circle.
 */
internal fun adaptiveIconOf(source: Bitmap): IconCompat {
    val edge = max(source.width, source.height)
    // Notifications are delivered over a ~1 MB binder transaction that has to
    // hold every icon in the message history at once; nothing here needs to be
    // bigger than the avatar size we asked for.
    val bounded = if (edge > Conversations.AVATAR_PX) {
        Bitmap.createScaledBitmap(source, Conversations.AVATAR_PX, Conversations.AVATAR_PX, true)
    } else {
        source
    }

    val padded = (max(bounded.width, bounded.height) * ADAPTIVE_SCALE).roundToInt()
    val output = Bitmap.createBitmap(padded, padded, Bitmap.Config.ARGB_8888)
    Canvas(output).drawBitmap(
        bounded,
        (padded - bounded.width) / 2f,
        (padded - bounded.height) / 2f,
        null,
    )
    return IconCompat.createWithAdaptiveBitmap(output)
}

private const val ADAPTIVE_SCALE = 1.5f
