package chat.bounce.data

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withTimeoutOrNull
import kotlin.time.Duration
import kotlin.time.Duration.Companion.minutes

/**
 * Suspends until the engine reports the network is usable.
 *
 * This exists because "the engine is running" and "the engine can reach a peer"
 * can be minutes apart, and nothing in the API surface says so. chat.Open
 * starts Tor on a goroutine and returns immediately, so an EngineClient exists
 * within milliseconds of launch, while the transport has no session until Tor
 * has bootstrapped and the onion service exists.
 *
 * In that window tor.Network.Dial does not fail - it blocks until the session
 * is there, and the bound call blocks with it. Every pairing call dials
 * directly (requestToSync, requestToAddUser, requestToManageEncryptedDevice),
 * so a user who scans a code in the first seconds after install would sit
 * behind a blocked engine call with nothing on screen to say why. The same wait
 * happens while the transport restarts Tor after an outage; there it gives up
 * after four minutes and the call fails as "could not connect to device", which
 * reads as a broken peer rather than a device that simply is not online yet.
 *
 * Gate those calls on this instead of on a non-null EngineClient, so the wait
 * happens here, where the caller can show it and bound it, rather than inside
 * an engine call that cannot be cancelled.
 *
 * Returns false if the network is still down after [timeout], which is a real
 * failure worth reporting - Tor genuinely cannot bootstrap on some networks.
 */
suspend fun awaitNetworkOnline(timeout: Duration = 3.minutes): Boolean =
    withTimeoutOrNull(timeout) {
        ChatRepository.networkOnline.first { it }
        true
    } ?: false
