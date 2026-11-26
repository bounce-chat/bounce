package chat.bounce.engine

import android.content.ContentResolver
import android.content.Context
import android.content.Intent
import android.net.Uri
import chat.bounce.goengine.FileStore
import java.io.File
import java.io.FileNotFoundException

/**
 * How the engine reads a file the user attached, from where the user keeps it.
 *
 * Android's pickers hand out content URIs, and only the ContentResolver can
 * open one. So the engine keeps the URI as the file's location and asks here
 * for a descriptor every time it needs to read: once to hash the file when it
 * is sent, and then once for each chunk a peer asks for, for as long as the
 * message exists. Nothing is copied.
 *
 * Reading a URI after the process that received it has gone takes a lasting
 * permission, and taking it is not done here: it is done when the file is
 * attached (see takeAttachment), because that is the only moment Android
 * offers it. What is done here is giving it back.
 *
 * THREADING - both methods are called by Go, on Go-owned threads, several at
 * once when several chunks are being served. A provider can take seconds to
 * open a file, so neither may touch anything the main thread could be holding.
 */
class ContentFileStore(context: Context) : FileStore {

    private val app = context.applicationContext

    override fun open(uri: String): Int {
        try {
            val descriptor = app.contentResolver.openFileDescriptor(Uri.parse(uri), "r")
                ?: throw FileNotFoundException("the provider returned no descriptor")
            // Hands the descriptor over. Go closes it, and nothing on this side
            // will: a ParcelFileDescriptor that was closed or collected here
            // would close a descriptor Go is still reading.
            return descriptor.detachFd()
        } catch (e: Exception) {
            // gobind carries nothing across but the message, so the kind of
            // failure goes into it. A lost permission and a deleted file read
            // the same to the engine and differently to whoever reads the log.
            throw Exception("${e.javaClass.simpleName}: ${e.message}")
        }
    }

    /**
     * The engine has deleted the last message that was served from [location].
     *
     * A content URI is the user's own file and stays where it is; only the
     * permission to read it is given back. A path is a file the app wrote: a
     * large attachment that was downloaded into the app's storage, or a copy an
     * earlier version of the app staged in its cache before sending. Those are
     * deleted, because once the message is gone nothing else refers to them and
     * the user has no way to reach them. A path anywhere else is left alone.
     */
    override fun release(location: String) {
        if (location.startsWith(CONTENT_PREFIX)) {
            app.contentResolver.releaseRead(Uri.parse(location))
            return
        }
        val file = File(location)
        if (file.isInside(app.filesDir) || file.isInside(app.cacheDir)) file.delete()
    }

    private fun File.isInside(directory: File): Boolean =
        canonicalPath.startsWith(directory.canonicalPath + File.separator)

    companion object {
        const val CONTENT_PREFIX = "content://"
    }
}

/**
 * Gives back the lasting permission to read [uri]. Android throws when none was
 * held, which is already the outcome wanted.
 */
fun ContentResolver.releaseRead(uri: Uri) {
    try {
        releasePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
    } catch (e: SecurityException) {
        // Nothing was held.
    }
}
