package chat.bounce.engine

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.net.Uri
import android.os.Handler
import android.os.HandlerThread
import android.os.ParcelFileDescriptor
import android.os.ProxyFileDescriptorCallback
import android.os.storage.StorageManager
import android.system.ErrnoException
import android.system.OsConstants
import android.util.Log
import chat.bounce.goengine.FileReader
import java.io.FileNotFoundException

/**
 * Serves a file the engine keeps in its own storage as if it were a plain file.
 *
 * Everything in the engine's blobs directory is encrypted, so nothing on this
 * side can open a blob for itself: a path to one leads to ciphertext. An image
 * is small enough to take whole from [EngineClient.fileData]. A video is not,
 * and the platform's player and thumbnail reader want something they can seek
 * in. So this provider answers `content://chat.bounce.blobs/<file id>` with a
 * seekable descriptor whose reads are answered by the engine, which decrypts
 * the part asked for. The same URI is what a file is copied out through when
 * the user saves it. Nothing is ever written out decrypted to make this work.
 *
 * Not exported, and no permission is ever granted on its URIs: only this app
 * resolves them. The descriptor itself is handed to the media service when a
 * video plays, which is the system's own process.
 *
 * A large file the user keeps somewhere else is not served from here. It is
 * not encrypted, and it is opened through the content URI it already has; see
 * [EngineClient.fileSource].
 */
class BlobProvider : ContentProvider() {

    override fun onCreate(): Boolean = true

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        if (mode != "r") throw FileNotFoundException("a file the engine keeps can only be read")
        val fileId = uri.lastPathSegment ?: throw FileNotFoundException("no file is named")
        val client = EngineHolder.client ?: throw FileNotFoundException("the engine is not running")

        // BLOCKING: a database read and an open. This runs on a binder thread
        // or on the caller's own, never on the main thread: every caller here
        // is already off it.
        val reader = try {
            client.openFile(fileId)
        } catch (e: Exception) {
            throw FileNotFoundException("the engine could not open the file: ${e.message}")
        }

        // One thread for each open file. The reads of one descriptor arrive in
        // order and each waits for the engine, so a single shared thread would
        // have a video that is playing wait behind every thumbnail being made.
        val reads = HandlerThread("bounce-blob-reads").apply { start() }
        return try {
            context!!.getSystemService(StorageManager::class.java).openProxyFileDescriptor(
                ParcelFileDescriptor.MODE_READ_ONLY,
                Reads(reader, reads),
                Handler(reads.looper),
            )
        } catch (e: Exception) {
            runCatching { reader.close() }
            reads.quitSafely()
            throw FileNotFoundException("could not serve the file: ${e.message}")
        }
    }

    /** Answers the reads on one descriptor from the engine, until the last holder of it lets go. */
    private class Reads(
        private val reader: FileReader,
        private val thread: HandlerThread,
    ) : ProxyFileDescriptorCallback() {

        override fun onGetSize(): Long = reader.size()

        override fun onRead(offset: Long, size: Int, data: ByteArray): Int {
            try {
                // gobind hands back null for no bytes, which is what the end of
                // the file reads as.
                val read: ByteArray = reader.readAt(offset, size.toLong()) ?: return 0
                System.arraycopy(read, 0, data, 0, read.size)
                return read.size
            } catch (e: Exception) {
                // A blob that does not decrypt has been damaged or tampered
                // with. The reader of the descriptor sees an I/O error.
                Log.w(TAG, "could not read a file the engine keeps: ${e.message}")
                throw ErrnoException("onRead", OsConstants.EIO)
            }
        }

        override fun onRelease() {
            runCatching { reader.close() }
            thread.quitSafely()
        }
    }

    // The files have no name or type here to report: the conversation knows
    // those, and it is the only thing that ever asks for one of these URIs.
    override fun getType(uri: Uri): String? = null

    override fun query(
        uri: Uri,
        projection: Array<out String>?,
        selection: String?,
        selectionArgs: Array<out String>?,
        sortOrder: String?,
    ): Cursor? = null

    override fun insert(uri: Uri, values: ContentValues?): Uri? = null

    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0

    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = 0

    companion object {
        /** Must match the provider's entry in the manifest. */
        private const val AUTHORITY = "chat.bounce.blobs"
        private const val TAG = "BounceBlobs"

        /** The URI a file the engine keeps is read through. */
        fun uriFor(fileId: String): Uri =
            Uri.Builder().scheme("content").authority(AUTHORITY).appendPath(fileId).build()
    }
}
