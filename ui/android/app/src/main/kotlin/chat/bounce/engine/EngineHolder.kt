package chat.bounce.engine

import android.content.Context
import android.util.Log
import androidx.core.content.pm.ShortcutManagerCompat
import chat.bounce.goengine.Engine
import chat.bounce.goengine.Goengine
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.first
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
 *
 * Everything the install keeps at rest is encrypted, so starting the engine
 * means unlocking the install first, and that can need the user: a password, a
 * recovery code, or a decision about data that can no longer be opened.
 * [lockReason] says which, the screens in ui/lock act on it, and until the
 * user has, there is no [client].
 */
object EngineHolder {

    private val startMutex = Mutex()

    @Volatile
    private var engine: Engine? = null

    @Volatile
    var bridge: EngineBridge? = null
        private set

    private val openClient = MutableStateFlow<EngineClient?>(null)

    val client: EngineClient? get() = openClient.value

    private val notOpenBecause = MutableStateFlow<String?>(null)

    /**
     * Why the install is not open: one of the Goengine.Error* codes, or the
     * text of an error that has no code. Null while the install is being
     * opened and once it is open.
     */
    val lockReason: StateFlow<String?> = notOpenBecause.asStateFlow()

    val isStarted: Boolean get() = client?.isReady == true

    /**
     * Starts the engine if it is not already running. Idempotent.
     *
     * [password] is empty on the first attempt, which is the only one the
     * service makes: an install with a password answers it with
     * Goengine.ErrorPasswordRequired in [lockReason], and the unlock screen
     * calls again with what the user typed. Returns null when the install did
     * not open, with the reason in [lockReason].
     *
     * BLOCKING: unlocks the install, opens the database, loads the Tor keys
     * and starts the network stack. Suspends on Dispatchers.IO; never call
     * from the main thread without it.
     */
    suspend fun start(context: Context, password: String = ""): EngineClient? = startMutex.withLock {
        client?.let { return@withLock it }
        notOpenBecause.value = null

        withContext(Dispatchers.IO) {
            // Seq needs the app context before any bound call.
            go.Seq.setContext(context.applicationContext)

            val newBridge = EngineBridge()
            val newEngine = Goengine.newEngine()
            val configDir = Goengine.defaultConfigDir()

            Log.i(TAG, "starting bounce engine, binding version ${Goengine.BindingVersion}")
            try {
                newEngine.start(
                    configDir,
                    password,
                    secretStore(configDir),
                    ContentFileStore(context),
                    newBridge,
                    newBridge,
                )
            } catch (e: Exception) {
                // The message is a code or an engine error, never anything the
                // user typed.
                Log.w(TAG, "the install did not open: ${e.message}")
                notOpenBecause.value = e.message?.takeIf { it.isNotBlank() } ?: e.toString()
                return@withContext null
            }

            engine = newEngine
            bridge = newBridge
            EngineClient(newEngine).also { openClient.value = it }
        }
    }

    /**
     * Suspends until the install is open. For an install that is waiting on
     * the user, that is when they have unlocked it from the app.
     */
    suspend fun awaitClient(): EngineClient = openClient.filterNotNull().first()

    /**
     * Opens a locked install with its recovery code and protects it afresh
     * with [newPassword], which may be empty for none. [start] is called with
     * the same password afterwards. Throws with a Goengine.Error* code as the
     * message when the code is not accepted.
     *
     * BLOCKING: stretches the new password.
     */
    suspend fun recover(context: Context, recoveryCode: String, newPassword: String) = startMutex.withLock {
        withContext(Dispatchers.IO) {
            go.Seq.setContext(context.applicationContext)
            val configDir = Goengine.defaultConfigDir()
            Goengine.recover(configDir, secretStore(configDir), recoveryCode, newPassword)
        }
    }

    /**
     * Deletes everything a locked install keeps on this device, for one that
     * nothing can open any more. The [start] that follows is a first run.
     */
    suspend fun reset(context: Context) = startMutex.withLock {
        withContext(Dispatchers.IO) {
            val app = context.applicationContext
            go.Seq.setContext(app)
            Goengine.reset(Goengine.defaultConfigDir())

            // The engine's own directory is gone. These are the copies of
            // attachments earlier versions of the app made outside it: large
            // ones they received, and ones they staged in the cache before
            // sending.
            File(app.filesDir, "downloads").deleteRecursively()
            File(app.cacheDir, "outgoing").deleteRecursively()

            // And the permissions kept so that large files could be served
            // from where the user keeps them, or downloaded to where the user
            // wanted them. No message refers to them now.
            runCatching {
                app.contentResolver.persistedUriPermissions.forEach { app.contentResolver.releasePersisted(it.uri) }
            }.onFailure { Log.w(TAG, "could not give back file permissions", it) }

            // And the launcher's shortcuts, which carry the names and pictures
            // of the conversations that were just deleted.
            runCatching {
                val ids = ShortcutManagerCompat.getShortcuts(
                    app,
                    ShortcutManagerCompat.FLAG_MATCH_DYNAMIC or ShortcutManagerCompat.FLAG_MATCH_CACHED,
                ).map { it.id }
                if (ids.isNotEmpty()) {
                    ShortcutManagerCompat.removeDynamicShortcuts(app, ids)
                    ShortcutManagerCompat.removeLongLivedShortcuts(app, ids)
                }
            }.onFailure { Log.w(TAG, "could not remove conversation shortcuts", it) }
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
            openClient.value = null
        }
    }

    private fun secretStore(configDir: String) = KeystoreSecretStore(File(configDir, KEYSTORE_FILE))

    private const val TAG = "BounceEngine"

    /** The engine's reset deletes this with the rest of its directory. */
    private const val KEYSTORE_FILE = "keystore.bin"
}
