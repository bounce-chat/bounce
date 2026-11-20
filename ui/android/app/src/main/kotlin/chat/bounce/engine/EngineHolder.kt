package chat.bounce.engine

import android.content.Context
import android.util.Log
import chat.bounce.goengine.Engine
import chat.bounce.goengine.Goengine
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

/**
 * Process-scoped owner of the Go engine.
 *
 * This must be a singleton for the life of the process, not a per-Activity or
 * per-Service object: the [EngineBridge] handed to Go is retained by Go's
 * refnum table, so rebinding it per component would leak whatever holds it.
 * Activities and ViewModels subscribe to the repository downstream; nothing
 * outside this package talks to Go directly.
 */
object EngineHolder {

    private val startMutex = Mutex()

    @Volatile
    private var engine: Engine? = null

    @Volatile
    var bridge: EngineBridge? = null
        private set

    @Volatile
    var client: EngineClient? = null
        private set

    val isStarted: Boolean get() = client?.isReady == true

    /**
     * Starts the engine if it is not already running. Idempotent.
     *
     * BLOCKING: opens the database, loads the Tor keys and starts the network
     * stack. Suspends on Dispatchers.IO; never call from the main thread
     * without it.
     */
    suspend fun start(context: Context): EngineClient = startMutex.withLock {
        client?.let { return@withLock it }

        withContext(Dispatchers.IO) {
            // Seq needs the app context before any bound call. Once per process.
            go.Seq.setContext(context.applicationContext)

            val newBridge = EngineBridge()
            val newEngine = Goengine.newEngine()

            Log.i(TAG, "starting bounce engine, binding version ${Goengine.BindingVersion}")
            newEngine.start(Goengine.defaultConfigDir(), newBridge, newBridge)

            engine = newEngine
            bridge = newBridge
            EngineClient(newEngine).also { client = it }
        }
    }

    /**
     * Stops the engine. Only the foreground service should call this - the app
     * is expected to keep running while the service holds a foreground
     * lifetime, so that messages continue to arrive.
     */
    suspend fun stop() = startMutex.withLock {
        withContext(Dispatchers.IO) {
            bridge?.close()
            engine?.stop()
            engine = null
            bridge = null
            client = null
        }
    }

    private const val TAG = "BounceEngine"
}
