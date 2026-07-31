package chat.bounce.ui.qr

import android.Manifest
import android.app.Activity
import android.content.ClipboardManager
import android.content.Context
import android.content.ContextWrapper
import android.content.pm.PackageManager
import android.graphics.ImageFormat
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ContentPaste
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import chat.bounce.R
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Full-screen QR reader shared by every pairing flow.
 *
 * [onResult] fires at most once with the decoded string; the caller owns what
 * happens next (`requestToAddUser`, `requestToSync`,
 * `requestToManageEncryptedDevice`). Pairing codes are also perfectly usable as
 * text - people send them over another messenger - so pasting is a first-class
 * path here, not a fallback for when the camera fails.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun QrScannerScreen(
    title: String,
    onResult: (String) -> Unit,
    onBack: () -> Unit,
) {
    val context = LocalContext.current

    var granted by remember {
        mutableStateOf(
            ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) ==
                PackageManager.PERMISSION_GRANTED
        )
    }
    // Distinguishes "not asked yet" from "asked and refused": only the second
    // warrants sending someone to system settings.
    var refused by remember { mutableStateOf(false) }

    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { result ->
        granted = result
        refused = !result
    }

    // Deliver exactly one result even though frames keep arriving until the
    // camera is torn down.
    val delivered = remember { AtomicBoolean(false) }
    var decoded by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(decoded) { decoded?.let(onResult) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(title) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.action_back),
                        )
                    }
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Spacer(Modifier.height(8.dp))

            if (granted) {
                CameraViewfinder(
                    onDecoded = { text ->
                        if (delivered.compareAndSet(false, true)) decoded = text
                    },
                    modifier = Modifier.fillMaxWidth().aspectRatio(1f),
                )
                Spacer(Modifier.height(12.dp))
                Text(
                    text = stringResource(R.string.qr_point_at_code),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                )
            } else {
                CameraPermissionRequest(
                    refused = refused,
                    onRequest = { permissionLauncher.launch(Manifest.permission.CAMERA) },
                    onOpenSettings = { context.openAppSettings() },
                )
            }

            Spacer(Modifier.height(24.dp))

            PasteCodeSection(
                onSubmit = { text ->
                    if (delivered.compareAndSet(false, true)) decoded = text
                },
            )

            Spacer(Modifier.height(24.dp))
        }
    }
}

@Composable
private fun CameraViewfinder(
    onDecoded: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current

    val previewView = remember {
        PreviewView(context).apply { scaleType = PreviewView.ScaleType.FILL_CENTER }
    }
    val analysisExecutor = remember { Executors.newSingleThreadExecutor() }

    // getInstance() returns a future that resolves once CameraX has initialised
    // its process-wide singleton; .get() is a blocking wait, hence IO.
    val provider by produceState<ProcessCameraProvider?>(null) {
        value = withContext(Dispatchers.IO) {
            runCatching { ProcessCameraProvider.getInstance(context).get() }.getOrNull()
        }
    }

    var failed by remember { mutableStateOf(false) }

    DisposableEffect(provider) {
        val cameraProvider = provider
        if (cameraProvider != null) {
            val preview = Preview.Builder().build().apply {
                setSurfaceProvider(previewView.surfaceProvider)
            }
            val analysis = ImageAnalysis.Builder()
                .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                .setOutputImageFormat(ImageAnalysis.OUTPUT_IMAGE_FORMAT_YUV_420_888)
                .build()
            analysis.setAnalyzer(analysisExecutor, QrAnalyzer(onDecoded))

            failed = runCatching {
                cameraProvider.unbindAll()
                cameraProvider.bindToLifecycle(
                    lifecycleOwner,
                    CameraSelector.DEFAULT_BACK_CAMERA,
                    preview,
                    analysis,
                )
            }.isFailure
        }

        onDispose {
            // bindToLifecycle keeps the camera alive for as long as the *Activity*
            // is resumed, so leaving this screen has to unbind explicitly or the
            // torch-adjacent privacy indicator stays lit on the thread list.
            provider?.unbindAll()
        }
    }

    DisposableEffect(analysisExecutor) {
        onDispose { analysisExecutor.shutdown() }
    }

    Box(
        modifier = modifier
            .background(Color.Black, RoundedCornerShape(12.dp)),
        contentAlignment = Alignment.Center,
    ) {
        if (failed) {
            Text(
                text = stringResource(R.string.qr_camera_unavailable),
                style = MaterialTheme.typography.bodyMedium,
                color = Color.White,
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(24.dp),
            )
        } else {
            AndroidView(factory = { previewView }, modifier = Modifier.fillMaxSize())
        }
    }
}

@Composable
private fun CameraPermissionRequest(
    refused: Boolean,
    onRequest: () -> Unit,
    onOpenSettings: () -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(vertical = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = stringResource(R.string.qr_permission_title),
            style = MaterialTheme.typography.titleMedium,
        )
        Spacer(Modifier.height(8.dp))
        Text(
            text = stringResource(R.string.qr_permission_body),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(16.dp))
        if (refused) {
            Text(
                text = stringResource(R.string.qr_permission_denied),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
                textAlign = TextAlign.Center,
            )
            Spacer(Modifier.height(12.dp))
            OutlinedButton(onClick = onOpenSettings) {
                Text(stringResource(R.string.qr_permission_settings))
            }
        } else {
            Button(onClick = onRequest) {
                Text(stringResource(R.string.qr_permission_grant))
            }
        }
    }
}

@Composable
private fun PasteCodeSection(onSubmit: (String) -> Unit) {
    val context = LocalContext.current
    var text by remember { mutableStateOf("") }
    var invalid by remember { mutableStateOf(false) }

    Column(Modifier.fillMaxWidth()) {
        Text(
            text = stringResource(R.string.qr_paste_instead),
            style = MaterialTheme.typography.titleSmall,
        )
        Spacer(Modifier.height(8.dp))
        OutlinedTextField(
            value = text,
            onValueChange = {
                // Codes are frequently pasted with a trailing newline from a
                // chat app; treating that as a typo would be unhelpful.
                text = it.trim()
                invalid = false
            },
            label = { Text(stringResource(R.string.qr_paste_label)) },
            singleLine = true,
            isError = invalid,
            supportingText = if (invalid) {
                { Text(stringResource(R.string.qr_invalid_code)) }
            } else {
                null
            },
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(8.dp))
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TextButton(onClick = { context.readClipboardText()?.let { text = it.trim() } }) {
                Icon(Icons.Filled.ContentPaste, contentDescription = null)
                Text(
                    text = stringResource(R.string.qr_paste_from_clipboard),
                    modifier = Modifier.padding(start = 8.dp),
                )
            }
            Spacer(Modifier.weight(1f))
            Button(
                enabled = text.isNotBlank(),
                onClick = {
                    if (looksLikeBounceCode(text)) onSubmit(text.trim()) else invalid = true
                },
            ) {
                Text(stringResource(R.string.qr_paste_submit))
            }
        }
    }
}

/**
 * Decodes QR codes out of the analyser's YUV frames.
 *
 * Only the luminance plane is touched: chroma is irrelevant to a black-and-white
 * symbol, and plane 0 of YUV_420_888 is exactly the greyscale image ZXing's
 * [PlanarYUVLuminanceSource] wants.
 */
private class QrAnalyzer(private val onDecoded: (String) -> Unit) : ImageAnalysis.Analyzer {

    private val reader = MultiFormatReader().apply {
        setHints(
            mapOf(
                DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE),
                DecodeHintType.TRY_HARDER to true,
            )
        )
    }

    override fun analyze(image: ImageProxy) {
        try {
            if (image.format != ImageFormat.YUV_420_888) return

            val luminance = image.luminancePlane() ?: return
            val source = PlanarYUVLuminanceSource(
                luminance,
                image.width,
                image.height,
                0,
                0,
                image.width,
                image.height,
                false,
            )

            val result = try {
                reader.decodeWithState(BinaryBitmap(HybridBinarizer(source)))
            } catch (_: Exception) {
                // NotFoundException on almost every frame is the normal case;
                // ChecksumException / FormatException mean a partial read.
                null
            } finally {
                reader.reset()
            }

            val text = result?.text?.trim().orEmpty()
            if (text.isNotEmpty() && looksLikeBounceCode(text)) onDecoded(text)
        } finally {
            image.close()
        }
    }
}

private fun ImageProxy.luminancePlane(): ByteArray? {
    val plane = planes.firstOrNull() ?: return null
    val buffer = plane.buffer.duplicate()
    val rowStride = plane.rowStride
    val pixelStride = plane.pixelStride
    val out = ByteArray(width * height)

    if (pixelStride == 1 && rowStride == width) {
        buffer.rewind()
        buffer.get(out, 0, minOf(out.size, buffer.remaining()))
        return out
    }

    // Padded rows (rowStride > width) are common on real hardware; copying row
    // by row is the only way to get a contiguous width*height buffer.
    val row = ByteArray(rowStride)
    for (y in 0 until height) {
        val start = y * rowStride
        if (start >= buffer.limit()) break
        buffer.position(start)
        val available = minOf(rowStride, buffer.remaining())
        buffer.get(row, 0, available)
        val destination = y * width
        for (x in 0 until width) {
            val source = x * pixelStride
            if (source >= available) break
            out[destination + x] = row[source]
        }
    }
    return out
}

private fun Context.readClipboardText(): String? {
    val manager = getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager ?: return null
    val clip = manager.primaryClip ?: return null
    if (clip.itemCount == 0) return null
    return clip.getItemAt(0).coerceToText(this)?.toString()?.takeIf { it.isNotBlank() }
}

private fun Context.openAppSettings() {
    val intent = android.content.Intent(
        android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
        android.net.Uri.fromParts("package", packageName, null),
    )
    // The camera prompt can be dismissed permanently, and there is no way back
    // from inside the app; this is the only remaining route.
    findActivity()?.startActivity(intent)
}

private fun Context.findActivity(): Activity? {
    var current: Context? = this
    while (current is ContextWrapper) {
        if (current is Activity) return current
        current = current.baseContext
    }
    return null
}
