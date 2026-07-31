package chat.bounce.ui.qr

import android.graphics.Bitmap
import android.graphics.Color as AndroidColor
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import chat.bounce.R
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import com.google.zxing.qrcode.decoder.ErrorCorrectionLevel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * QR rendering for the three pairing flows (add contact, link device, manage an
 * encrypted device).
 *
 * ZXing's `android-integration` artifact is deliberately not a dependency - it
 * drags in an Activity and an intent contract we do not want - so the
 * BitMatrix-to-Bitmap step lives here.
 */

/**
 * Renders [content] as a [sizePx] x [sizePx] QR bitmap.
 *
 * Error correction is left at M: the payload is ~90 characters, so a higher
 * level would push the symbol into a denser version for no practical gain,
 * and a denser symbol is harder to read off a phone screen at arm's length.
 *
 * @throws com.google.zxing.WriterException if [content] cannot be encoded.
 */
fun encodeQr(content: String, sizePx: Int, dark: Int, light: Int): Bitmap {
    val hints = mapOf(
        EncodeHintType.ERROR_CORRECTION to ErrorCorrectionLevel.M,
        EncodeHintType.CHARACTER_SET to "UTF-8",
        // ZXing's default quiet zone is 4 modules, which wastes a lot of an
        // already small on-screen symbol. The composable draws its own white
        // border, which serves the same purpose.
        EncodeHintType.MARGIN to 1,
    )

    val matrix = QRCodeWriter().encode(content, BarcodeFormat.QR_CODE, sizePx, sizePx, hints)
    val width = matrix.width
    val height = matrix.height

    val pixels = IntArray(width * height)
    for (y in 0 until height) {
        val offset = y * width
        for (x in 0 until width) {
            pixels[offset + x] = if (matrix[x, y]) dark else light
        }
    }

    return Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888).apply {
        setPixels(pixels, 0, width, 0, 0, width, height)
    }
}

/**
 * A QR code sized to fill its slot, always black-on-white.
 *
 * Theme colours are not used on purpose: a light-on-dark symbol is within the
 * QR spec but a meaningful number of scanner apps refuse it, and this code is
 * read by whatever the other person happens to have installed.
 */
@Composable
fun QrCodeImage(content: String, modifier: Modifier = Modifier) {
    BoxWithConstraints(
        modifier = modifier
            .background(Color.White, RoundedCornerShape(12.dp))
            .padding(12.dp),
        contentAlignment = Alignment.Center,
    ) {
        // Encoding at exactly the on-screen pixel size keeps module edges on
        // pixel boundaries; upscaling a smaller bitmap visibly smears them.
        val sizePx = minOf(constraints.maxWidth, constraints.maxHeight).coerceIn(256, 1024)

        val bitmap by produceState<Bitmap?>(null, content, sizePx) {
            value = withContext(Dispatchers.Default) {
                runCatching {
                    encodeQr(content, sizePx, AndroidColor.BLACK, AndroidColor.WHITE)
                }.getOrNull()
            }
        }

        val rendered = bitmap
        if (rendered == null) {
            Box(Modifier.fillMaxWidth().aspectRatio(1f), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }
        } else {
            Image(
                bitmap = rendered.asImageBitmap(),
                contentDescription = stringResource(R.string.qr_code_description),
                modifier = Modifier.fillMaxWidth().aspectRatio(1f),
                contentScale = ContentScale.Fit,
                filterQuality = FilterQuality.None,
            )
        }
    }
}

/**
 * Cheap shape check for the strings produced by the engine's
 * `GetNewAddUserString` / `GetNewSyncString`: a v3 onion service ID, a colon,
 * and a hex secret.
 *
 * This exists so the scanner can ignore unrelated QR codes (a Wi-Fi config, a
 * URL on a poster) and keep looking, instead of firing the one-shot result
 * callback with something the engine will only reject. Bounds are loose so a
 * change to the secret length on the Go side does not silently break scanning.
 */
fun looksLikeBounceCode(text: String): Boolean =
    BOUNCE_CODE_SHAPE.matches(text.trim())

private val BOUNCE_CODE_SHAPE = Regex("^[a-z2-7]{16,128}:[0-9a-f]{16,128}$", RegexOption.IGNORE_CASE)
