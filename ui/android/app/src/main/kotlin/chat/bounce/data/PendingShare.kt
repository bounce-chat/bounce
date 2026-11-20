package chat.bounce.data

import android.net.Uri
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/**
 * Content handed to Bounce from another app's share sheet.
 *
 * @param threadId the conversation it is addressed to, or null until the user
 *   has picked one. A direct-share tap names the thread up front; a plain "share
 *   to Bounce" does not.
 */
data class SharePayload(
    val uris: List<Uri> = emptyList(),
    val text: String = "",
    val threadId: String? = null,
) {
    val isEmpty: Boolean get() = uris.isEmpty() && text.isBlank()
}

/**
 * The one share currently in flight.
 *
 * Process-scoped rather than passed through navigation arguments: content URIs
 * do not belong in a route string, and the read permission the share grants is
 * held by the Activity, so the payload has to outlive the composable that
 * receives it without ever being serialized.
 *
 * A [StateFlow] rather than a one-shot event because the conversation may
 * already be open when the share arrives, in which case its ViewModel is
 * retained and its `init` will not run again - it has to be able to observe the
 * payload appearing rather than only find it on construction.
 *
 * Only one share is tracked. Two share sheets racing into the same app is not a
 * real scenario, and the alternative is a queue whose entries can outlive the
 * URI grants that make them readable.
 */
object PendingShare {

    private val _pending = MutableStateFlow<SharePayload?>(null)
    val pending: StateFlow<SharePayload?> = _pending.asStateFlow()

    fun offer(payload: SharePayload) {
        if (payload.isEmpty) return
        _pending.value = payload
    }

    /** Records which conversation the user picked, once they have picked one. */
    fun addressTo(threadId: String) {
        _pending.update { it?.copy(threadId = threadId) }
    }

    /**
     * Takes the payload if it is addressed to [threadId], leaving it in place
     * otherwise. Claiming clears it, so a share is applied exactly once even
     * though several collectors may see it.
     */
    fun claim(threadId: String): SharePayload? {
        val payload = _pending.value ?: return null
        if (payload.threadId != threadId) return null
        _pending.value = null
        return payload
    }

    /** Abandons the share, e.g. when the user backs out of the picker. */
    fun clear() {
        _pending.value = null
    }
}
