package chat.bounce.engine

import android.util.Log
import chat.bounce.goengine.EventSink
import chat.bounce.goengine.NotificationSink
import kotlinx.coroutines.channels.Channel

/**
 * The Go-facing side of the boundary: implements the two gomobile interfaces
 * and does nothing but hand work to a channel.
 *
 * THREADING - every method here runs on a Go-owned OS thread that gomobile
 * attached to the JVM. Never the main looper, and several can run at once
 * because the engine has a goroutine per peer. The emitting goroutine is
 * blocked for the duration of the call, so each body must be exactly one
 * non-blocking trySend. Anything that could block - a lock, runBlocking, a
 * withContext - risks deadlocking across JNI against a Kotlin thread that is
 * itself inside an Engine call, and no Kotlin-side tool will diagnose it.
 *
 * The channel is UNLIMITED rather than a SharedFlow with DROP_OLDEST: dropping
 * a message under load is far worse for a chat app than a transient memory
 * blip, and UNLIMITED preserves emission order, which the notification
 * correlation in [EngineSignal] depends on.
 */
class EngineBridge : EventSink, NotificationSink {

    private val channel = Channel<EngineSignal>(Channel.UNLIMITED)

    /** Drained by exactly one collector, in [EngineEventPump]. */
    val signals: Channel<EngineSignal> get() = channel

    private fun send(signal: EngineSignal) {
        val result = channel.trySend(signal)
        if (result.isFailure) {
            // Cannot happen with an UNLIMITED channel unless it has been closed,
            // which only happens at shutdown.
            Log.w(TAG, "dropped engine signal ${signal::class.simpleName}")
        }
    }

    // --- EventSink ---------------------------------------------------------

    override fun onEvent(kind: String, payload: String) {
        send(EngineSignal.Raw(kind, payload))
    }

    override fun onTyping(userID: String, threadID: String, typing: Boolean) {
        send(EngineSignal.Typing(userID, threadID, typing))
    }

    override fun onPresence(id: String, kind: String, online: Boolean) {
        send(EngineSignal.Presence(id, kind, online))
    }

    override fun onProgress(id: String, kind: String, fraction: Double) {
        send(EngineSignal.Progress(id, kind, fraction))
    }

    // --- NotificationSink --------------------------------------------------

    override fun post(
        id: String,
        title: String,
        content: String,
        openThread: String,
        icon: ByteArray?,
    ) {
        send(EngineSignal.NotificationPost(id, title, content, openThread, icon))
    }

    override fun clear(id: String) {
        send(EngineSignal.NotificationClear(id))
    }

    fun close() {
        channel.close()
    }

    private companion object {
        const val TAG = "BounceBridge"
    }
}
