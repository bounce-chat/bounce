package chat.bounce.data

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.Log
import android.util.LruCache
import chat.bounce.engine.EngineClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

/**
 * Decoded attachment and avatar bitmaps, keyed by the engine's file UUID.
 *
 * Process-scoped rather than per-screen: the same avatar appears in the thread
 * list, the conversation header and every incoming bubble, and a phone camera
 * photo is 12 megapixels - 48 MB decoded at full resolution, which is more than
 * the whole heap budget for several of them.
 *
 * The bytes come from the engine, never from disk: what the engine keeps is
 * encrypted there, and [EngineClient.fileData] hands the file back decrypted.
 * That is a blocking call and a copy of the whole file across JNI, which is why
 * every decode is cached here and nothing is decoded twice.
 */
object ImageCache {

    /**
     * The conventional one-eighth of the heap. Sized in KB because LruCache's
     * size is an Int and byte counts of large bitmaps overflow it.
     */
    private val cache = object : LruCache<String, Bitmap>(
        ((Runtime.getRuntime().maxMemory() / 1024L) / 8L).coerceIn(4 * 1024L, 64 * 1024L).toInt()
    ) {
        override fun sizeOf(key: String, value: Bitmap): Int = value.byteCount / 1024
    }

    /**
     * Stops a LazyColumn that binds the same avatar into eight rows at once from
     * decoding it eight times. Striped rather than one lock per file so the map
     * of locks cannot grow without bound, and rather than a single lock so two
     * unrelated photos still decode in parallel.
     */
    private val locks = Array(8) { Mutex() }

    private fun lockFor(key: String): Mutex = locks[(key.hashCode() ushr 1) % locks.size]

    /**
     * Returns the decoded image, downscaled so neither side exceeds
     * [maxDimension], or null when the file has not arrived yet.
     *
     * A null is normal, not an error: attachments download in the background and
     * a message can be rendered long before its images arrive.
     */
    suspend fun load(
        client: EngineClient,
        fileId: String,
        maxDimension: Int = 1024,
    ): Bitmap? {
        if (fileId.isEmpty()) return null
        // The size is part of the key because a 96px avatar and a full-screen
        // view of the same photo are different bitmaps; keying on the UUID alone
        // would serve whichever was asked for first.
        val key = "$fileId@$maxDimension"
        cache.get(key)?.let { return it }

        return withContext(Dispatchers.IO) {
            lockFor(key).withLock {
                cache.get(key) ?: run {
                    // Throws until the file is complete, which is the normal
                    // case named above and not worth a log line.
                    val data = runCatching { client.fileData(fileId) }.getOrNull() ?: return@withLock null
                    decode(fileId, data, maxDimension)?.also { cache.put(key, it) }
                }
            }
        }
    }

    /** Drops every cached size of one file. */
    fun evict(fileId: String) {
        val prefix = "$fileId@"
        cache.snapshot().keys.forEach { if (it.startsWith(prefix)) cache.remove(it) }
    }

    fun clear() = cache.evictAll()

    private fun decode(fileId: String, data: ByteArray, maxDimension: Int): Bitmap? {
        if (data.isEmpty()) return null

        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(data, 0, data.size, bounds)
        if (bounds.outWidth <= 0 || bounds.outHeight <= 0) {
            Log.w(TAG, "not a decodable image: $fileId")
            return null
        }

        val options = BitmapFactory.Options().apply {
            inSampleSize = sampleSizeFor(bounds.outWidth, bounds.outHeight, maxDimension)
            inPreferredConfig = Bitmap.Config.ARGB_8888
        }
        return try {
            BitmapFactory.decodeByteArray(data, 0, data.size, options)
        } catch (e: OutOfMemoryError) {
            // Recoverable: drop what we are holding and let the caller show the
            // fallback rather than take the process down over one photo.
            Log.w(TAG, "out of memory decoding $fileId", e)
            cache.evictAll()
            null
        }
    }

    /**
     * The largest power of two that keeps both sides at or above [maxDimension],
     * which is what BitmapFactory rounds to anyway. Decoding is subsampled in
     * the decoder, so the full-resolution bitmap is never allocated.
     */
    private fun sampleSizeFor(width: Int, height: Int, maxDimension: Int): Int {
        var sample = 1
        var longest = maxOf(width, height)
        while (longest / 2 >= maxDimension) {
            longest /= 2
            sample *= 2
        }
        return sample
    }

    private const val TAG = "BounceImages"
}
