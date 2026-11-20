package chat.bounce.notifications

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.os.Build
import chat.bounce.R

/**
 * The two parent notification channels, created once per process.
 *
 * Channel *settings* are immutable after creation - the system keeps whatever
 * the user (or the first `createNotificationChannel` call) established, and
 * later calls can only change the name, description and group. Anything that
 * has to be true for conversations to work - `setAllowBubbles`, the importance
 * floor - therefore has to be right the first time, which is why these ids are
 * new rather than the ones the old Fyne/Java build used: that build's message
 * channel was created without bubble support, and an upgrade in place (same
 * applicationId, `chat.bounce`) would inherit it permanently.
 */
object NotificationChannels {

    /** Ongoing "Bounce is running" notification that keeps the engine alive. */
    const val SERVICE_CHANNEL_ID = "chat.bounce.channel.service"

    /** Everything a person sends you: DMs, group messages, group invites. */
    const val MESSAGES_CHANNEL_ID = "chat.bounce.channel.messages"

    /** Channels created by the old Java client, removed on first run of this one. */
    private val LEGACY_CHANNEL_IDS = listOf(
        "chat.bounce.foreground_service_channel",
        "chat.bounce.message_channel_v3",
    )

    /**
     * Vibration kept from the old client so the app still "feels" the same to
     * anyone upgrading: short-gap double buzz.
     */
    private val MESSAGE_VIBRATION = longArrayOf(0, 400, 200, 400)

    fun ensure(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return

        val service = NotificationChannel(
            SERVICE_CHANNEL_ID,
            context.getString(R.string.service_channel_name),
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = context.getString(R.string.service_channel_description)
            // It is a permanent notification; a permanent badge on the launcher
            // icon next to it would be noise, and would hide the real unread count.
            setShowBadge(false)
            enableVibration(false)
            enableLights(false)
            setSound(null, null)
        }

        val messages = NotificationChannel(
            MESSAGES_CHANNEL_ID,
            context.getString(R.string.messages_channel_name),
            NotificationManager.IMPORTANCE_HIGH,
        ).apply {
            description = context.getString(R.string.messages_channel_description)
            setShowBadge(true)
            enableVibration(true)
            vibrationPattern = MESSAGE_VIBRATION
            enableLights(true)
            // Bounce exists to keep message content off other people's machines;
            // leaking it onto a locked screen would undo that. VISIBILITY_PRIVATE
            // still shows that a message arrived, it just hides who from and what.
            lockscreenVisibility = Notification.VISIBILITY_PRIVATE
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                setAllowBubbles(true)
            }
        }

        manager.createNotificationChannels(listOf(service, messages))

        LEGACY_CHANNEL_IDS.forEach { manager.deleteNotificationChannel(it) }
    }

    /**
     * The channel a conversation's notifications post to.
     *
     * Deliberately always the one parent channel. From API 30 the system derives
     * a *conversation* child channel per shortcut id on its own, and that is what
     * backs the per-conversation Priority/Default/Silent controls; publishing our
     * own per-thread channels would compete with it, produce an unmanageable list
     * in system settings, and permanently retain a channel for every contact the
     * user has ever spoken to.
     */
    @Suppress("UNUSED_PARAMETER")
    fun channelIdFor(threadId: String): String = MESSAGES_CHANNEL_ID
}
