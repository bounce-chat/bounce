package chat.bounce.data

import android.graphics.Bitmap
import android.graphics.BitmapShader
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Matrix
import android.graphics.Paint
import android.graphics.Shader
import chat.bounce.engine.EngineClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.nio.ByteBuffer
import java.util.UUID
import kotlin.math.abs

/**
 * Avatar generation, bit-compatible with the desktop client.
 *
 * A contact who has never set a picture must show the same colour on the phone
 * as on the laptop, because that colour is the only thing distinguishing two
 * people with the same initial. The algorithm is therefore reproduced from
 * ui/default_image.go exactly, including Go's truncating float-to-uint8
 * conversion - rounding instead would shift channels by one and break the match.
 */
object Avatars {

    /** HSV derived from the UUID's raw bytes: hue from b[0..1], saturation from b[3]. */
    fun colorForId(id: String): Int {
        val bytes = uuidBytes(id)
        val hue = (((bytes[0].toInt() and 0xFF) shl 8) or (bytes[1].toInt() and 0xFF)) % 360
        val saturation = ((bytes[3].toInt() and 0xFF) % 10 + 65) / 100.0
        return hsvToColor(hue.toDouble(), saturation, 0.8)
    }

    /**
     * One or two characters for the generated avatar: the first character of the
     * first word, plus the first character of the last word when there is more
     * than one and it is a letter.
     */
    fun initialsFor(name: String): String {
        val parts = name.split(' ', '\t', '\n', ' ').filter { it.isNotBlank() }
        val first = parts.firstOrNull()?.let(::firstCharacter) ?: return "?"
        if (parts.size == 1) return first.uppercase()
        val last = firstCharacter(parts.last())
        // A trailing "(work)" or an emoji makes a poor second initial.
        if (last == null || !last.all { it.isLetter() }) return first.uppercase()
        return (first + last).uppercase()
    }

    /**
     * The avatar for a user or group.
     *
     * [fileIds] is the engine's Images list, oldest first, so it is searched
     * newest first and the first file that is actually on disk and decodable
     * wins - images arrive asynchronously and the newest is often still
     * downloading.
     *
     * The result is always [sizePx] square with a transparent background outside
     * the circle. The old client baked the theme background colour into the
     * corners, which shows as a pale square behind the avatar wherever Android
     * applies its own circular mask - notification icons and shortcuts
     * especially.
     */
    suspend fun avatarBitmap(
        client: EngineClient,
        fileIds: List<String>,
        fallbackId: String,
        fallbackName: String,
        sizePx: Int,
    ): Bitmap {
        val size = sizePx.coerceAtLeast(1)
        for (fileId in fileIds.asReversed()) {
            val decoded = ImageCache.load(client, fileId, size) ?: continue
            return withContext(Dispatchers.Default) { circularCrop(decoded, size) }
        }
        return withContext(Dispatchers.Default) { generatedAvatar(fallbackId, fallbackName, size) }
    }

    private fun circularCrop(source: Bitmap, size: Int): Bitmap {
        val output = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(output)
        val radius = size / 2f

        // Centre-crop: scale on the shorter side so the circle is fully covered,
        // then centre the overflow.
        val scale = size.toFloat() / minOf(source.width, source.height)
        val shader = BitmapShader(source, Shader.TileMode.CLAMP, Shader.TileMode.CLAMP).apply {
            setLocalMatrix(
                Matrix().apply {
                    setScale(scale, scale)
                    postTranslate(
                        (size - source.width * scale) / 2f,
                        (size - source.height * scale) / 2f,
                    )
                }
            )
        }

        canvas.drawCircle(radius, radius, radius, Paint(Paint.ANTI_ALIAS_FLAG).apply {
            this.shader = shader
        })
        return output
    }

    private fun generatedAvatar(id: String, name: String, size: Int): Bitmap {
        val output = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(output)
        val radius = size / 2f

        canvas.drawCircle(radius, radius, radius, Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = colorForId(id)
            style = Paint.Style.FILL
        })

        val initials = initialsFor(name)
        val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = Color.WHITE
            textAlign = Paint.Align.CENTER
            textSize = size / 2f
        }
        // Two wide glyphs at half the diameter can run past the circle's edge.
        val maxWidth = size * 0.7f
        val measured = paint.measureText(initials)
        if (measured > maxWidth) paint.textSize *= maxWidth / measured

        val metrics = paint.fontMetrics
        canvas.drawText(initials, radius, radius - (metrics.ascent + metrics.descent) / 2f, paint)
        return output
    }

    /**
     * The 16 bytes behind a canonical UUID string. IDs that are not UUIDs still
     * get a stable colour rather than a crash: nothing about an avatar is worth
     * taking a screen down for.
     */
    private fun uuidBytes(id: String): ByteArray = try {
        val uuid = UUID.fromString(id)
        ByteBuffer.allocate(16)
            .putLong(uuid.mostSignificantBits)
            .putLong(uuid.leastSignificantBits)
            .array()
    } catch (_: IllegalArgumentException) {
        ByteArray(16) { i -> if (id.isEmpty()) 0 else id[i % id.length].code.toByte() }
    }

    private fun firstCharacter(word: String): String? {
        if (word.isEmpty()) return null
        // By code point, so an astral-plane character is not split into half a
        // surrogate pair.
        return String(Character.toChars(word.codePointAt(0)))
    }

    private fun hsvToColor(hue: Double, saturation: Double, value: Double): Int {
        val c = value * saturation
        val x = c * (1 - abs((hue / 60.0) % 2.0 - 1))
        val m = value - c

        val r: Double
        val g: Double
        val b: Double
        when {
            hue < 60 -> { r = c; g = x; b = 0.0 }
            hue < 120 -> { r = x; g = c; b = 0.0 }
            hue < 180 -> { r = 0.0; g = c; b = x }
            hue < 240 -> { r = 0.0; g = x; b = c }
            hue < 300 -> { r = x; g = 0.0; b = c }
            else -> { r = c; g = 0.0; b = x }
        }

        // toInt() truncates, matching Go's uint8(...) conversion.
        return Color.argb(
            255,
            ((r + m) * 255).toInt(),
            ((g + m) * 255).toInt(),
            ((b + m) * 255).toInt(),
        )
    }
}
