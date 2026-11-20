package chat.bounce.data

import android.graphics.Bitmap
import android.media.MediaMetadataRetriever
import android.util.Log
import android.util.LruCache
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.File
import java.util.Locale

/**
 * What a blob turns out to be, once something has actually opened it.
 *
 * [width] and [height] are the video's own dimensions in display orientation,
 * not the thumbnail's: a bubble needs the aspect ratio to reserve space before
 * any frame has been decoded, and that ratio must not change when the same file
 * is probed again at a different [VideoThumbnails.probe] size.
 */
data class VideoInfo(
    val thumbnail: Bitmap?,
    val durationMs: Long,
    val width: Int,
    val height: Int,
)

/**
 * Poster frames and metadata for attachment blobs that turn out to be video.
 *
 * The engine writes attachments as bare UUIDs with no extension, so there is no
 * name to consult and no MIME type on disk - the only way to learn that a blob
 * is a playable video is to hand it to a decoder and see whether the decoder
 * accepts it. [MediaMetadataRetriever] is that decoder, which makes probing far
 * more expensive than the BitmapFactory header read in [ImageCache]: it starts a
 * real codec instance. Every answer, including "this is not a video", is
 * therefore cached.
 *
 * Process-scoped for the same reason as [ImageCache] - a conversation scrolled
 * up and back down must not re-decode what it already knows.
 */
object VideoThumbnails {

    /**
     * A sixteenth of the heap rather than the conventional eighth [ImageCache]
     * takes, because the two caches share one heap and video attachments are far
     * rarer than photos and avatars. Sized in KB for the same reason: LruCache's
     * size is an Int and byte counts of large bitmaps overflow it.
     */
    private val cache = object : LruCache<String, VideoInfo>(
        ((Runtime.getRuntime().maxMemory() / 1024L) / 16L).coerceIn(2 * 1024L, 32 * 1024L).toInt()
    ) {
        // The +1 floor matters: a video whose frame could not be decoded holds a
        // null bitmap and would otherwise cost nothing, so no number of them
        // would ever trigger a trim.
        override fun sizeOf(key: String, value: VideoInfo): Int =
            1 + (value.thumbnail?.byteCount ?: 0) / 1024
    }

    /**
     * Blobs that are not video, mapped to the file length they were rejected at.
     *
     * Separate from [cache] rather than a sentinel inside it because a negative
     * has no bitmap and so no byte cost - it would sit in a byte-sized LRU
     * forever and crowd out real thumbnails. Bounded by entry count instead.
     *
     * The length is the invalidation: attachments stream in over Tor, and a blob
     * that was a truncated prefix when it was first probed becomes a decodable
     * video once the transfer finishes. Remembering "not a video" against the
     * length we saw means the completed file gets one more chance, while a
     * document - whose length never changes again - is probed exactly once.
     *
     * Keyed on path alone: whether a file is video does not depend on the size
     * the caller asked the thumbnail to be scaled to.
     */
    private val notVideo = LruCache<String, Long>(256)

    /**
     * Stops two bubbles bound to the same file from starting two decoders.
     * Striped like [ImageCache] so the lock table cannot grow without bound, and
     * with a happy side effect that matters more here than it does for images:
     * at most eight [MediaMetadataRetriever] instances can be open at once, well
     * under any device's codec limit, even though Dispatchers.IO would otherwise
     * happily run sixty-four probes in parallel.
     */
    private val locks = Array(8) { Mutex() }

    private fun lockFor(key: String): Mutex = locks[(key.hashCode() ushr 1) % locks.size]

    /**
     * Returns null when [path] is not a playable video.
     *
     * Null is also the answer for a blob that has not finished downloading yet,
     * which is normal rather than an error; that verdict is not cached, so the
     * next call after the transfer completes will look again.
     */
    suspend fun probe(path: String, maxDimension: Int = 1024): VideoInfo? {
        if (path.isEmpty()) return null
        // The requested size is part of the key for the same reason as in
        // ImageCache: a 96px preview and a full-screen poster of one file are
        // different bitmaps.
        val key = "$path@$maxDimension"
        cache.get(key)?.let { return it }

        return withContext(Dispatchers.IO) {
            lockFor(key).withLock {
                cache.get(key)?.let { return@withLock it }

                // Stat inside the IO dispatch, not on the fast path above, so a
                // recomposing bubble never touches the filesystem from the main
                // thread.
                val file = File(path)
                val length = file.length()
                if (!file.isFile || length == 0L) return@withLock null
                if (notVideo.get(path) == length) return@withLock null

                val info = probeBlocking(path, maxDimension)
                if (info == null) notVideo.put(path, length) else cache.put(key, info)
                info
            }
        }
    }

    /** Drops every cached size of one file, and any memory of it not being video. */
    fun evict(path: String) {
        val prefix = "$path@"
        cache.snapshot().keys.forEach { if (it.startsWith(prefix)) cache.remove(it) }
        notVideo.remove(path)
    }

    fun clear() {
        cache.evictAll()
        notVideo.evictAll()
    }

    /**
     * "1:05", "12:04", "1:02:03" - the overlay label on a video bubble.
     *
     * Seconds truncate rather than round, matching every media player: a clip of
     * 64.9s reads 1:04, never 1:05 on a timeline that never reaches it.
     */
    fun formatDuration(ms: Long): String {
        val total = (ms / 1000L).coerceAtLeast(0L)
        val hours = total / 3600L
        val minutes = (total % 3600L) / 60L
        val seconds = total % 60L
        // Locale.US, not the default: this is a timecode, not a localized
        // number, and %d under an Arabic or Persian locale emits Arabic-Indic
        // digits that would read as a different script from the rest of the UI.
        return if (hours > 0L) {
            String.format(Locale.US, "%d:%02d:%02d", hours, minutes, seconds)
        } else {
            String.format(Locale.US, "%d:%02d", minutes, seconds)
        }
    }

    /**
     * The whole probe, blocking, on one retriever.
     *
     * Release is an explicit try/finally rather than `use {}`. Kotlin's
     * AutoCloseable.use would compile - MediaMetadataRetriever gained
     * AutoCloseable in API 29 and minSdk is 29 - but its close() is declared to
     * throw IOException, so `use` would force a checked-exception catch around
     * what is really just native teardown. release() has been the API since
     * forever and is unambiguous. Getting this wrong leaks a hardware decoder
     * per attachment, and a device runs out of codec instances long before it
     * runs out of memory.
     */
    private fun probeBlocking(path: String, maxDimension: Int): VideoInfo? {
        val retriever = MediaMetadataRetriever()
        try {
            // This call is the sniff. There is no extension to trust, so a
            // document, an audio file or a half-written blob is identified by
            // the decoder refusing it.
            retriever.setDataSource(path)

            val hasVideo = retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_HAS_VIDEO)
            // An audio file parses perfectly and answers "no" here. Absent is
            // not the same as "no", though - a few containers omit the key
            // entirely - so an absent value defers to whether a frame decodes.
            if (hasVideo != null && !hasVideo.equals("yes", ignoreCase = true)) return null

            val rotation = retriever
                .extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_ROTATION)
                ?.toIntOrNull() ?: 0
            // A phone video held upright is stored 1920x1080 with rotation 90;
            // sizing a bubble from the raw keys would lay it out sideways. The
            // frames the retriever returns are already rotated, so display
            // orientation is what both the caller and the scaler need.
            val normalized = ((rotation % 360) + 360) % 360
            val quarterTurned = normalized == 90 || normalized == 270
            val storedWidth = retriever
                .extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_WIDTH)
                ?.toIntOrNull() ?: 0
            val storedHeight = retriever
                .extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_HEIGHT)
                ?.toIntOrNull() ?: 0
            val width = if (quarterTurned) storedHeight else storedWidth
            val height = if (quarterTurned) storedWidth else storedHeight

            val durationMs = retriever
                .extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)
                ?.toLongOrNull()
                ?.coerceAtLeast(0L) ?: 0L

            val frame = extractFrame(retriever, width, height, maxDimension)
            // No frame and no claim of a video track is no evidence at all; a
            // file that only got this far by parsing as some container is not
            // something we should offer to play. A file that does claim a video
            // track but will not yield a frame (DRM, a codec this device lacks)
            // is still a video - the caller can show duration and a play badge
            // over a placeholder.
            if (frame == null && hasVideo == null) return null

            return VideoInfo(
                thumbnail = frame,
                durationMs = durationMs,
                // Metadata first, the decoded frame only as a fallback for
                // containers that report no dimensions: the frame's size is the
                // scaled size, so it is right about aspect but wrong about the
                // video's actual resolution.
                width = if (width > 0) width else (frame?.width ?: 0),
                height = if (height > 0) height else (frame?.height ?: 0),
            )
        } catch (e: Exception) {
            // Everything MediaMetadataRetriever rejects - wrong magic bytes, an
            // unsupported codec, a truncated download - arrives as an unchecked
            // native exception. A wrong guess about a blob must be a null, never
            // a crashed conversation. Logged at debug because "this attachment
            // is a PDF" is the expected case, not a fault.
            Log.d(TAG, "not a playable video: $path (${e.javaClass.simpleName}: ${e.message})")
            return null
        } catch (e: OutOfMemoryError) {
            // Recoverable in the same way ImageCache treats it: drop what we
            // hold rather than take the process down over one frame.
            Log.w(TAG, "out of memory probing $path", e)
            cache.evictAll()
            return null
        } finally {
            retriever.release()
        }
    }

    /**
     * The first decodable frame, no larger than [maxDimension] on its long side.
     *
     * [displayWidth] and [displayHeight] may be 0 when the container did not say.
     */
    private fun extractFrame(
        retriever: MediaMetadataRetriever,
        displayWidth: Int,
        displayHeight: Int,
        maxDimension: Int,
    ): Bitmap? {
        val limit = maxDimension.coerceAtLeast(1)
        val (targetWidth, targetHeight) = targetSize(displayWidth, displayHeight, limit)

        // getScaledFrameAtTime is API 27+ and minSdk is 29, so no version guard
        // is needed - but the try is not about the API level. It scales inside
        // the decoder, which is the difference between allocating a 1024px
        // bitmap and allocating the 33 MB one a 4K frame would need first.
        try {
            retriever.getScaledFrameAtTime(
                0L,
                MediaMetadataRetriever.OPTION_CLOSEST_SYNC,
                targetWidth,
                targetHeight,
            )?.let { return it }
        } catch (e: RuntimeException) {
            // Some OEM decoders fail only on the scaled path; the unscaled one
            // below often still works, so this is not yet a verdict on the file.
            Log.d(TAG, "scaled frame extraction failed, retrying unscaled", e)
        }

        // No arguments means "any representative frame", which is a looser
        // request than a sync frame at t=0 and succeeds on some clips where the
        // call above returns null. The manual downscale is what
        // getScaledFrameAtTime would have done for us.
        val full = retriever.getFrameAtTime() ?: return null
        return downscale(full, limit)
    }

    /**
     * The frame size to ask the decoder for, preserving aspect ratio and never
     * upscaling - a 320x240 clip should not be blown up to 1024 just because a
     * full-screen view asked.
     */
    private fun targetSize(width: Int, height: Int, limit: Int): Pair<Int, Int> {
        // Unknown dimensions: a square box. getScaledFrameAtTime fits the frame
        // inside it and preserves the aspect ratio (its Bitmap may legitimately
        // come back smaller than what was requested), so the only cost is that a
        // very wide clip lands a little short of the limit.
        if (width <= 0 || height <= 0) return limit to limit
        val longest = maxOf(width, height)
        if (longest <= limit) return width to height
        val scale = limit.toDouble() / longest
        return Pair(
            (width * scale).toInt().coerceAtLeast(1),
            (height * scale).toInt().coerceAtLeast(1),
        )
    }

    private fun downscale(frame: Bitmap, limit: Int): Bitmap {
        if (maxOf(frame.width, frame.height) <= limit) return frame
        val (width, height) = targetSize(frame.width, frame.height, limit)
        val scaled = Bitmap.createScaledBitmap(frame, width, height, true)
        // createScaledBitmap can hand back the original instance; only recycle a
        // full-size frame that nothing else can be holding.
        if (scaled !== frame) frame.recycle()
        return scaled
    }

    private const val TAG = "BounceVideo"
}
