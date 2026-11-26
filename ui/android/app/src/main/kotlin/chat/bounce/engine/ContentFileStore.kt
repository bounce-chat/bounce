package chat.bounce.engine

import android.content.ContentResolver
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.DocumentsContract
import android.util.Log
import chat.bounce.goengine.FileStore
import java.io.File
import java.io.FileNotFoundException

/**
 * How the engine reaches a file where the user keeps it: one the user attached,
 * or the place the user chose to download a large file to.
 *
 * Android's pickers hand out content URIs, and only the ContentResolver can
 * open one. So the engine keeps the URI as the file's location and asks here
 * for a descriptor every time it needs one. For a file that was sent that is
 * once to hash it, and then once for each chunk a peer asks for, for as long as
 * the message exists. For one being received it is once for each chunk that
 * arrives, written straight into the document the user created. Nothing is
 * copied either way.
 *
 * Reaching a URI after the process that received it has gone takes a lasting
 * permission, and taking it is not done here: it is done when the file is
 * attached (see takeAttachment) or the destination is picked (see
 * takeDownloadDestination), because that is the only moment Android offers it.
 * What is done here is giving it back.
 *
 * THREADING - every method is called by Go, on Go-owned threads, several at
 * once when several chunks are on the move. A provider can take seconds to open
 * a file, so none may touch anything the main thread could be holding.
 */
class ContentFileStore(context: Context) : FileStore {

    private val app = context.applicationContext

    override fun open(uri: String): Int = descriptor(uri, "r")

    /**
     * "rw" and not "rwt": each chunk of a download opens the document again and
     * writes at its own offset, so nothing already written may be dropped.
     */
    override fun create(uri: String): Int = descriptor(uri, "rw")

    private fun descriptor(uri: String, mode: String): Int {
        try {
            val descriptor = app.contentResolver.openFileDescriptor(Uri.parse(uri), mode)
                ?: throw FileNotFoundException("the provider returned no descriptor")
            // Hands the descriptor over. Go closes it, and nothing on this side
            // will: a ParcelFileDescriptor that was closed or collected here
            // would close a descriptor Go is still using.
            return descriptor.detachFd()
        } catch (e: Exception) {
            // gobind carries nothing across but the message, so the kind of
            // failure goes into it. A lost permission and a deleted file read
            // the same to the engine and differently to whoever reads the log.
            throw Exception("${e.javaClass.simpleName}: ${e.message}")
        }
    }

    /**
     * The engine has given up on a download that was being written into [uri]:
     * it was cancelled, or its message was deleted before it finished. Half a
     * file is no use to anyone, so the document goes.
     *
     * This is the one place the app deletes something the user can see, so it
     * checks for itself that the document is one it was downloading into. Only
     * a download destination is ever held with permission to write; a file the
     * user attached is held to read. Without that permission nothing is
     * deleted, whatever the engine asked.
     */
    override fun remove(uri: String) {
        val document = Uri.parse(uri)
        val resolver = app.contentResolver
        val downloadingInto = resolver.persistedUriPermissions.any { it.uri == document && it.isWritePermission }
        if (!downloadingInto) {
            Log.w(TAG, "not deleting a document that is not a download destination, authority=${document.authority}")
            return
        }
        try {
            DocumentsContract.deleteDocument(resolver, document)
        } catch (e: FileNotFoundException) {
            // The user already deleted it, which is the outcome wanted.
        } catch (e: Exception) {
            resolver.releasePersisted(document)
            throw Exception("${e.javaClass.simpleName}: ${e.message}")
        }
        resolver.releasePersisted(document)
    }

    /**
     * The engine has deleted the last message that was served from [location].
     *
     * A content URI is the user's own file and stays where it is; only the
     * permission to reach it is given back. A path is a file an earlier version
     * of the app wrote: a large attachment it downloaded into the app's storage,
     * or a copy it staged in its cache before sending. Those are deleted,
     * because once the message is gone nothing else refers to them and the user
     * has no way to reach them. A path anywhere else is left alone.
     */
    override fun release(location: String) {
        if (location.startsWith(CONTENT_PREFIX)) {
            app.contentResolver.releasePersisted(Uri.parse(location))
            return
        }
        val file = File(location)
        if (file.isInside(app.filesDir) || file.isInside(app.cacheDir)) file.delete()
    }

    private fun File.isInside(directory: File): Boolean =
        canonicalPath.startsWith(directory.canonicalPath + File.separator)

    companion object {
        const val CONTENT_PREFIX = "content://"
        private const val TAG = "BounceFiles"
    }
}

/**
 * Gives back the lasting permission to reach [uri], to read it and, for a
 * download destination, to write it. Android throws when none was held, which
 * is already the outcome wanted.
 */
fun ContentResolver.releasePersisted(uri: Uri) {
    try {
        releasePersistableUriPermission(
            uri,
            Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
        )
    } catch (e: SecurityException) {
        // Nothing was held.
    }
}
