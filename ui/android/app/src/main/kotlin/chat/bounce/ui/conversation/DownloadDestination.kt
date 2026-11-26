package chat.bounce.ui.conversation

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.DocumentsContract
import android.system.Os
import android.system.OsConstants
import android.util.Log
import androidx.activity.result.contract.ActivityResultContracts

/**
 * The system's save dialog, asking where a large file should be downloaded to.
 *
 * A file too large to embed is never kept in the app's storage. The user says
 * where it goes, the engine writes it there as it arrives, and it stays there
 * as the user's own file. The dialog is told to offer only places on this
 * device: a download is written a chunk at a time, at whatever offset each
 * chunk belongs, over minutes or days, which cloud storage cannot take.
 *
 * The input is the name to suggest, and the result is the document the dialog
 * created, already there and empty.
 */
internal class CreateLocalDocument : ActivityResultContracts.CreateDocument(ANY_MIME) {
    override fun createIntent(context: Context, input: String): Intent =
        super.createIntent(context, input).putExtra(Intent.EXTRA_LOCAL_ONLY, true)
}

/**
 * Checks that a large file can be downloaded into the document the user just
 * created, and keeps the permission to do it.
 *
 * It has to be a plain file on this device that can be written at any offset,
 * from a provider known to stay reachable: once the download finishes the file
 * is served to others from here, exactly as an attachment that stays in place
 * is, so the same providers qualify (see [staysReadable]). The dialog already
 * hides cloud storage, and this turns away anything else that got through.
 *
 * The permission is the lasting kind, to read and to write. A download can
 * outlive the process that started it, and the service carries it on after a
 * restart of the phone with no screen open. It is given back when the message
 * is deleted, or with the document itself if the download is cancelled; see
 * ContentFileStore.
 *
 * BLOCKING: asks another app's provider. Call it off the main thread.
 *
 * @return false when the place cannot be used. The caller then removes the
 *   empty document with [discardDownloadDestination] and tells the user.
 */
internal fun takeDownloadDestination(context: Context, destination: Uri): Boolean {
    val resolver = context.contentResolver
    if (!staysReadable(destination)) {
        Log.w(TAG, "not a place a download can be written to, authority=${destination.authority}")
        return false
    }
    val regularFile = try {
        resolver.openFileDescriptor(destination, "rw")?.use {
            OsConstants.S_ISREG(Os.fstat(it.fileDescriptor).st_mode)
        } ?: false
    } catch (e: Exception) {
        Log.w(TAG, "could not open a download destination, authority=${destination.authority}", e)
        false
    }
    if (!regularFile) return false
    return try {
        resolver.takePersistableUriPermission(
            destination,
            Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
        )
        true
    } catch (e: SecurityException) {
        Log.w(TAG, "no lasting permission offered for a download destination, authority=${destination.authority}")
        false
    }
}

/**
 * Removes the empty document the save dialog created, for a place that turned
 * out not to be usable. Failing to is harmless: an empty file is left behind.
 */
internal fun discardDownloadDestination(context: Context, destination: Uri) {
    runCatching { DocumentsContract.deleteDocument(context.contentResolver, destination) }
        .onFailure { Log.w(TAG, "could not remove an unused download destination, authority=${destination.authority}") }
}

private const val ANY_MIME = "*/*"
private const val TAG = "BounceDownload"
