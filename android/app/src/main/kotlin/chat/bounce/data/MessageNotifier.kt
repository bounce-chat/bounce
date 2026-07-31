package chat.bounce.data

import chat.bounce.engine.EngineSignal

/**
 * The notification side of the engine's signal stream.
 *
 * Declared in the data layer, implemented in chat.bounce.notifications, so that
 * [EngineEventPump] can hand off a notification without depending on Android's
 * NotificationManager - and so the pump can be exercised with a no-op notifier.
 *
 * The engine, not this app, decides *whether* to notify: postNotification is
 * only called after the engine has applied its mute, foreground and
 * cross-device DND rules. The implementation's job is to render the
 * notification, not to second-guess it.
 *
 * onNotificationPost is suspending because building a MessagingStyle
 * notification means reading avatars off disk, which must not run on the main
 * thread. It is called from the pump's single ordered collector, so
 * implementations do not need to be reentrant.
 */
interface MessageNotifier {
    suspend fun onNotificationPost(signal: EngineSignal.NotificationPost)
    fun onNotificationClear(messageId: String)
}

/** For tests, and for any component that drives the pump without a UI. */
object NoOpMessageNotifier : MessageNotifier {
    override suspend fun onNotificationPost(signal: EngineSignal.NotificationPost) = Unit
    override fun onNotificationClear(messageId: String) = Unit
}
