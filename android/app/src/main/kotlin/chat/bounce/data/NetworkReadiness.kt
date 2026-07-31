package chat.bounce.data

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withTimeoutOrNull
import kotlin.time.Duration
import kotlin.time.Duration.Companion.minutes

/**
 * Suspends until the engine reports the network is usable.
 *
 * This exists because "the engine is running" and "the engine can reach a peer"
 * are minutes apart, and nothing in the API surface says so. chat.Open starts
 * Tor on a goroutine and returns immediately, so an EngineClient exists within
 * milliseconds of launch, while TorNetwork.tor and .onion stay nil until the
 * whole bootstrap and hidden-service publish finishes.
 *
 * In that window TorNetwork.Dial does not block - it returns
 * "cannot dial while network is not started" instantly. Every pairing call
 * dials (requestToSync, requestToAddUser, requestToManageEncryptedDevice), so a
 * user who scans a code in the first seconds after install gets an immediate
 * "could not connect to device", which reads as a broken peer rather than a
 * device that simply is not online yet.
 *
 * Gate those calls on this instead of on a non-null EngineClient.
 *
 * Returns false if the network is still down after [timeout], which is a real
 * failure worth reporting - Tor genuinely cannot bootstrap on some networks.
 */
suspend fun awaitNetworkOnline(timeout: Duration = 3.minutes): Boolean =
    withTimeoutOrNull(timeout) {
        ChatRepository.networkOnline.first { it }
        true
    } ?: false
