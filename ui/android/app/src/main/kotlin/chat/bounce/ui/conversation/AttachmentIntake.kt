package chat.bounce.ui.conversation

import android.content.ContentResolver
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.DocumentsContract
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.system.Os
import android.system.OsConstants
import android.util.Log
import chat.bounce.goengine.Goengine
import java.util.UUID

/**
 * What the engine does with an attachment that is over its embed limit.
 *
 * At or under the limit none of this applies: every small file is read into the
 * engine's own storage when the message is sent, wherever it came from. Over the
 * limit the file has to be served to whoever asks for it, for as long as the
 * message exists, and whether that can be done from where the file already is
 * depends on where it came from - which only this side knows.
 */
enum class LargeFile {
    /**
     * Served from where the user keeps it, with no copy. The app holds the
     * lasting permission to read the URI, so it stays readable across restarts.
     */
    IN_PLACE,

    /**
     * Copied into the engine's storage when the message is sent, and deleted
     * with the message. For anything that is not known to be a plain file on
     * this device: cloud storage opens slowly and through a temporary copy of
     * its own, and an unfamiliar provider is not trusted to keep working.
     */
    COPY,

    /**
     * Left out of the message. A file shared in from another app can be read
     * only while the share lasts, so a large one could not be served later.
     */
    REFUSE,
}

/** What came of the user trying to attach one file. */
sealed interface Intake {
    data class Ready(val attachment: PendingAttachment) : Intake

    /** Shared in from another app and over the embed limit. */
    data class TooLargeToShare(val name: String) : Intake

    /** There are no bytes to read, as with a document that only exists in a cloud editor. */
    data class Unreadable(val name: String) : Intake
}

/**
 * Looks at a file the user picked or shared, and decides how it will be sent.
 *
 * Nothing is copied and, for most files, nothing is opened: the name and size
 * come from the provider's own listing. A file is opened here only when the
 * provider gives no size, or when a large file is about to be left in place and
 * has to be shown to be a real file first.
 *
 * BLOCKING: asks another app's provider, which for a cloud provider can mean
 * the network. Call it off the main thread.
 *
 * @param shared true for a file another app shared in, false for one picked
 *   with this app's own pickers.
 */
internal fun takeAttachment(context: Context, uri: Uri, shared: Boolean): Intake {
    val resolver = context.contentResolver
    var listedName: String? = null
    var size = UNKNOWN_SIZE
    var virtual = false

    // Every column is asked for, not the three wanted: MediaStore throws on a
    // projection that names a column it does not have.
    runCatching {
        resolver.query(uri, null, null, null, null)?.use { cursor ->
            if (!cursor.moveToFirst()) return@use
            cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                .let { if (it >= 0 && !cursor.isNull(it)) listedName = cursor.getString(it) }
            cursor.getColumnIndex(OpenableColumns.SIZE)
                .let { if (it >= 0 && !cursor.isNull(it)) size = cursor.getLong(it) }
            cursor.getColumnIndex(DocumentsContract.Document.COLUMN_FLAGS).let {
                if (it >= 0 && !cursor.isNull(it)) {
                    virtual = cursor.getInt(it) and DocumentsContract.Document.FLAG_VIRTUAL_DOCUMENT != 0
                }
            }
        }
    }.onFailure { Log.w(TAG, "could not list an attachment, authority=${uri.authority}", it) }

    val name = listedName?.takeIf { it.isNotBlank() }
        ?: uri.lastPathSegment?.substringAfterLast('/')?.takeIf { it.isNotBlank() }
        ?: UNNAMED_ATTACHMENT

    // A virtual document has no bytes of its own, only a way to ask its app to
    // export it as something else. Opening it fails, so say so now rather than
    // when the message is sent.
    if (virtual) return Intake.Unreadable(name)

    val limit = Goengine.EmbeddedFileLimit
    val local = !shared && staysReadable(uri)
    var regularFile = false
    if (size == UNKNOWN_SIZE || (local && size > limit)) {
        val descriptor = try {
            resolver.openFileDescriptor(uri, "r")
        } catch (e: Exception) {
            Log.w(TAG, "could not open an attachment, authority=${uri.authority}", e)
            null
        } ?: return Intake.Unreadable(name)
        descriptor.use {
            regularFile = runCatching { OsConstants.S_ISREG(Os.fstat(it.fileDescriptor).st_mode) }.getOrDefault(false)
            // Stays unknown for a pipe. The engine then counts as it reads.
            if (size == UNKNOWN_SIZE) size = it.statSize
        }
    }

    if (shared && size > limit) return Intake.TooLargeToShare(name)

    var whenLarge = if (shared) LargeFile.REFUSE else LargeFile.COPY
    var tookPermission = false
    if (local && regularFile && size > limit) {
        val alreadyHeld = resolver.persistedUriPermissions.any { it.isReadPermission && it.uri == uri }
        try {
            resolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
            whenLarge = LargeFile.IN_PLACE
            // An earlier message may already be served from this same file. Its
            // permission is that message's to give back, not this attachment's.
            tookPermission = !alreadyHeld
        } catch (e: SecurityException) {
            // Android will not let this one be kept, so it is copied instead.
            Log.w(TAG, "no lasting permission offered, authority=${uri.authority}")
        }
    }

    return Intake.Ready(
        PendingAttachment(
            id = UUID.randomUUID().toString(),
            uri = uri,
            name = name,
            size = size,
            isImage = resolver.getType(uri)?.startsWith("image/") == true,
            whenLarge = whenLarge,
            tookPermission = tookPermission,
        ),
    )
}

/**
 * Whether [uri] names a plain file on this device that stays readable for as
 * long as the app keeps its permission.
 *
 * These are the providers that were tested for exactly that: read at any
 * offset, permission kept through a restart of the phone, and opened in a few
 * milliseconds. Everything else is copied, which always works - so a provider
 * missing from this list costs storage, never a broken attachment. Google Drive
 * is deliberately not here: it keeps its permission too, but takes seconds to
 * open a file and writes out a temporary copy of its own each time.
 */
private fun staysReadable(uri: Uri): Boolean {
    if (uri.scheme != ContentResolver.SCHEME_CONTENT) return false
    return when (uri.authority) {
        DOWNLOADS_AUTHORITY, EXTERNAL_STORAGE_AUTHORITY -> true
        // The photo picker: content://media/picker/<user>/<provider>/media/<id>.
        // The provider segment tells an item on the device from one that only
        // exists in a cloud library, which is not on this device to be served.
        MediaStore.AUTHORITY -> uri.pathSegments.let {
            it.firstOrNull() == "picker" && it.getOrNull(2) == LOCAL_PICKER_PROVIDER
        }
        else -> false
    }
}

/** What the engine opens. A file: URI is given as its path, which the engine opens itself. */
internal val PendingAttachment.location: String
    get() = if (uri.scheme == ContentResolver.SCHEME_FILE) uri.path.orEmpty() else uri.toString()

/** The name of a file whose provider gives it none. */
internal const val UNNAMED_ATTACHMENT = "attachment"

private const val UNKNOWN_SIZE = -1L
private const val DOWNLOADS_AUTHORITY = "com.android.providers.downloads.documents"
private const val EXTERNAL_STORAGE_AUTHORITY = "com.android.externalstorage.documents"
private const val LOCAL_PICKER_PROVIDER = "com.android.providers.media.photopicker"
private const val TAG = "BounceAttachment"
