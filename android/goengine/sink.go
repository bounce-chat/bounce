package goengine

// EventSink receives engine events. It is implemented in Kotlin and handed to
// Engine.Start; gobind emits it as a Java interface and routes calls back
// through JNI into that implementation.
//
// THREADING CONTRACT - read this before implementing:
//
//   - These methods run on Go-owned OS threads that gobind attaches to the JVM.
//     They are NEVER on the Android main looper, and several may run at once:
//     the engine has a goroutine per peer.
//   - The emitting goroutine is BLOCKED for the duration of the call. An
//     implementation must be a single non-blocking handoff - Channel.trySend -
//     and nothing else. No runBlocking, no withContext, no lock that a thread
//     currently inside an Engine call could be holding: that deadlocks across
//     JNI in a way no Kotlin-side tool will diagnose.
//   - Do not touch Views. Parse the JSON in the collector, not here.
//
// Kept deliberately small because gobind requires a Java implementation to
// supply every method, so each one added here is a source-breaking change.
type EventSink interface {
	// OnEvent delivers a structured event. kind is the chat.UI method name;
	// payload is a JSON object, or "" for events that carry no data.
	OnEvent(kind string, payload string)

	// OnTyping, OnPresence and OnProgress are carved out of OnEvent because
	// they fire per keystroke, per peer state change and per download tick
	// respectively. Routing them through JSON would allocate on every tick for
	// no benefit.
	OnTyping(userID string, threadID string, typing bool)
	// kind is "user" or "device".
	OnPresence(id string, kind string, online bool)
	// kind is "file" or "initialsync"; fraction is 0..1.
	OnProgress(id string, kind string, fraction float64)
}

// NotificationSink receives notification requests from the engine. It replaces
// the two Java threads in the old GoForegroundService that blocked forever
// inside Goservice.getNotification() and getNotificationToClear().
//
// Same threading contract as EventSink: post to a handler and return.
type NotificationSink interface {
	// Post asks for a notification. id identifies the notification for a later
	// Clear; openThread is the thread UUID to open when it is tapped; icon is
	// PNG bytes of the sender or group avatar, and may be empty.
	Post(id string, title string, content string, openThread string, icon []byte)

	// Clear dismisses a previously posted notification by id.
	Clear(id string)
}
