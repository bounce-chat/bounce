package chat.bounce.ui.conversation

import android.graphics.Bitmap
import kotlin.math.PI
import kotlin.math.absoluteValue
import kotlin.math.cos
import kotlin.math.pow
import kotlin.math.withSign

/**
 * BlurHash decoder (woltapp/blurhash), written out here because no BlurHash
 * dependency is available and the engine attaches a hash to every image.
 *
 * A hash is a base-83 string: one character of component counts, one of the AC
 * quantisation ceiling, four for the DC (average) colour, then two per AC
 * coefficient. Decoding is an inverse DCT over that handful of cosine basis
 * functions, evaluated straight into a tiny bitmap - the output is meant to be
 * scaled up and is blurry by construction, so 20-30 px a side is plenty.
 */
object BlurHash {

    private const val ALPHABET =
        "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#\$%*+,-.:;=?@[]^_{|}~"

    /** Returns null for a malformed or empty hash rather than throwing. */
    fun decode(hash: String, width: Int, height: Int, punch: Float = 1f): Bitmap? {
        if (hash.length < 6 || width <= 0 || height <= 0) return null

        val sizeFlag = decode83(hash, 0, 1) ?: return null
        val componentsX = sizeFlag % 9 + 1
        val componentsY = sizeFlag / 9 + 1
        if (hash.length != 4 + 2 * componentsX * componentsY) return null

        val quantisedMax = decode83(hash, 1, 2) ?: return null
        val maxAc = (quantisedMax + 1) / 166f * punch

        val components = Array(componentsX * componentsY) { FloatArray(3) }
        components[0] = decodeDc(decode83(hash, 2, 6) ?: return null)
        for (i in 1 until components.size) {
            val value = decode83(hash, 4 + i * 2, 6 + i * 2) ?: return null
            components[i] = decodeAc(value, maxAc)
        }

        val pixels = IntArray(width * height)
        for (y in 0 until height) {
            for (x in 0 until width) {
                var r = 0f
                var g = 0f
                var b = 0f
                for (j in 0 until componentsY) {
                    for (i in 0 until componentsX) {
                        val basis = cos(PI * x * i / width).toFloat() * cos(PI * y * j / height).toFloat()
                        val component = components[j * componentsX + i]
                        r += component[0] * basis
                        g += component[1] * basis
                        b += component[2] * basis
                    }
                }
                pixels[y * width + x] =
                    (0xFF shl 24) or (linearToSrgb(r) shl 16) or (linearToSrgb(g) shl 8) or linearToSrgb(b)
            }
        }
        return Bitmap.createBitmap(pixels, width, height, Bitmap.Config.ARGB_8888)
    }

    private fun decode83(source: String, from: Int, to: Int): Int? {
        var value = 0
        for (i in from until to) {
            val index = ALPHABET.indexOf(source[i])
            if (index < 0) return null
            value = value * 83 + index
        }
        return value
    }

    private fun decodeDc(value: Int) = floatArrayOf(
        srgbToLinear(value shr 16 and 0xFF),
        srgbToLinear(value shr 8 and 0xFF),
        srgbToLinear(value and 0xFF),
    )

    private fun decodeAc(value: Int, maxAc: Float) = floatArrayOf(
        signedPow((value / (19 * 19) - 9) / 9f) * maxAc,
        signedPow((value / 19 % 19 - 9) / 9f) * maxAc,
        signedPow((value % 19 - 9) / 9f) * maxAc,
    )

    /** The encoder squares coefficients to gain precision near zero. */
    private fun signedPow(value: Float): Float = value.absoluteValue.pow(2f).withSign(value)

    private fun srgbToLinear(value: Int): Float {
        val v = value / 255f
        return if (v <= 0.04045f) v / 12.92f else ((v + 0.055f) / 1.055f).pow(2.4f)
    }

    private fun linearToSrgb(value: Float): Int {
        val v = value.coerceIn(0f, 1f)
        val srgb = if (v <= 0.0031308f) v * 12.92f else 1.055f * v.pow(1f / 2.4f) - 0.055f
        return (srgb * 255f + 0.5f).toInt().coerceIn(0, 255)
    }
}
