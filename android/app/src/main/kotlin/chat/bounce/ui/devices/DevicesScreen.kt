package chat.bounce.ui.devices

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.data.RepositoryEffect
import chat.bounce.engine.Device
import chat.bounce.engine.EngineHolder
import chat.bounce.ui.qr.QrCodeImage
import chat.bounce.ui.theme.LocalBounceColors
import kotlinx.coroutines.launch

/**
 * The devices attached to this profile.
 *
 * A Bounce profile is a set of devices that all hold the same keys; there is no
 * server copy. Revoking one therefore has to roll the profile's keys, which is
 * why it is behind a confirmation rather than a swipe.
 *
 * [onLinkDevice] is the host's route to [chat.bounce.ui.qr.QrScannerScreen]; it
 * is used for the encrypted-device path, which is the only one where *this*
 * device does the scanning. The ordinary "link a device" direction shows a code
 * for the new device to read, so it stays inline.
 *
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DevicesScreen(
    onBack: () -> Unit,
    onLinkDevice: () -> Unit,
    /** True once the scanner has actually read a code, not merely been opened. */
    scanSubmitted: Boolean = false,
) {
    val scope = rememberCoroutineScope()
    val devices by ChatRepository.devices.collectAsStateWithLifecycle()
    val snackbars = remember { SnackbarHostState() }

    // Saveable: the encrypted-device path leaves for the scanner destination and
    // comes back, and dropping the user on the plain device list would lose the
    // outcome of what they just did.
    var linking by rememberSaveable { mutableStateOf(false) }
    var editing by remember { mutableStateOf<Device?>(null) }

    // Consume the system back gesture while the link panel is open so it closes
    // the panel rather than leaving the screen behind it.
    BackHandler(enabled = linking) { linking = false }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        stringResource(
                            if (linking) R.string.devices_link_title else R.string.devices_title
                        )
                    )
                },
                navigationIcon = {
                    IconButton(onClick = { if (linking) linking = false else onBack() }) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.action_back),
                        )
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbars) },
    ) { padding ->
        if (linking) {
            LinkDevicePanel(
                modifier = Modifier.padding(padding),
                onScanEncrypted = onLinkDevice,
                scanSubmitted = scanSubmitted,
                snackbars = snackbars,
            )
        } else {
            DeviceList(
                devices = devices,
                modifier = Modifier.padding(padding),
                onLink = { linking = true },
                onSelect = { editing = it },
            )
        }
    }

    editing?.let { device ->
        DeviceDialog(
            device = device,
            onDismiss = { editing = null },
            // The engine calls are launched from the screen's scope, not the
            // dialog's: dismissing the dialog tears its scope down, and both of
            // these actions dismiss as they fire.
            onRename = { name ->
                scope.launch { runCatching { EngineHolder.client?.renameDevice(device.id, name) } }
            },
            onRevoke = {
                scope.launch { runCatching { EngineHolder.client?.revokeDevice(device.id) } }
            },
        )
    }
}

@Composable
private fun DeviceList(
    devices: List<Device>,
    onLink: () -> Unit,
    onSelect: (Device) -> Unit,
    modifier: Modifier = Modifier,
) {
    // Own device first, then everything that is currently reachable.
    val ordered = remember(devices) {
        devices.sortedWith(
            compareByDescending<Device> { it.local }
                .thenByDescending { it.online }
                .thenByDescending { it.lastSeen }
        )
    }

    LazyColumn(modifier.fillMaxSize()) {
        item {
            Button(
                onClick = onLink,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 16.dp),
            ) {
                Text(stringResource(R.string.link_device))
            }
            HorizontalDivider()
        }

        if (ordered.size <= 1) {
            item {
                Text(
                    text = stringResource(R.string.devices_empty),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.fillMaxWidth().padding(24.dp),
                )
            }
        }

        items(ordered, key = { it.id }) { device ->
            DeviceRow(device = device, onClick = { onSelect(device) })
        }
    }
}

@Composable
private fun DeviceRow(device: Device, onClick: () -> Unit) {
    val colors = LocalBounceColors.current
    val dot = when {
        device.local -> colors.deviceLocal
        device.online -> colors.online
        else -> colors.offline
    }

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(10.dp).background(dot, CircleShape))
        Column(Modifier.weight(1f).padding(start = 16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = device.name.ifBlank { device.address },
                    style = MaterialTheme.typography.bodyLarge,
                )
                if (device.encrypted) {
                    Icon(
                        imageVector = Icons.Filled.Lock,
                        contentDescription = stringResource(R.string.devices_encrypted),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(start = 6.dp).size(14.dp),
                    )
                }
            }
            Text(
                text = device.subtitle(),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun Device.subtitle(): String = when {
    local -> stringResource(R.string.devices_this_device)
    online -> stringResource(R.string.devices_online)
    lastSeen <= 0L -> stringResource(R.string.devices_never_seen)
    else -> stringResource(R.string.devices_last_seen, relativeSeen(lastSeen))
}

@Composable
private fun relativeSeen(unixSeconds: Long): String {
    val elapsed = (System.currentTimeMillis() / 1000L - unixSeconds).coerceAtLeast(0L)
    return when {
        elapsed < 60 -> stringResource(R.string.devices_seen_now)
        elapsed < 60 * 60 -> stringResource(R.string.devices_seen_minutes, elapsed / 60)
        elapsed < 24 * 60 * 60 -> stringResource(R.string.devices_seen_hours, elapsed / 3600)
        else -> stringResource(R.string.devices_seen_days, elapsed / 86_400)
    }
}

@Composable
private fun DeviceDialog(
    device: Device,
    onDismiss: () -> Unit,
    onRename: (String) -> Unit,
    onRevoke: () -> Unit,
) {
    var name by remember(device.id) { mutableStateOf(device.name) }
    var confirmingRevoke by remember(device.id) { mutableStateOf(false) }

    if (confirmingRevoke) {
        AlertDialog(
            onDismissRequest = { confirmingRevoke = false },
            title = {
                Text(
                    stringResource(
                        R.string.devices_revoke_title,
                        device.name.ifBlank { device.address },
                    )
                )
            },
            text = { Text(stringResource(R.string.devices_revoke_body)) },
            confirmButton = {
                TextButton(
                    colors = ButtonDefaults.textButtonColors(
                        contentColor = MaterialTheme.colorScheme.error,
                    ),
                    onClick = {
                        confirmingRevoke = false
                        onDismiss()
                        onRevoke()
                    },
                ) {
                    Text(stringResource(R.string.devices_revoke))
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmingRevoke = false }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
        return
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.devices_details_title)) },
        text = {
            Column {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it.trimStart().take(MAX_DEVICE_NAME_LENGTH) },
                    label = { Text(stringResource(R.string.devices_device_name)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(12.dp))
                Text(
                    text = device.address,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (!device.local) {
                    Spacer(Modifier.height(16.dp))
                    TextButton(
                        colors = ButtonDefaults.textButtonColors(
                            contentColor = MaterialTheme.colorScheme.error,
                        ),
                        onClick = { confirmingRevoke = true },
                    ) {
                        Text(stringResource(R.string.devices_revoke))
                    }
                }
            }
        },
        confirmButton = {
            TextButton(
                enabled = name.isNotBlank() && name != device.name,
                onClick = {
                    val wanted = name.trim()
                    onDismiss()
                    onRename(wanted)
                },
            ) {
                Text(stringResource(R.string.action_save))
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) }
        },
    )
}

// --- linking ----------------------------------------------------------------

@Composable
private fun LinkDevicePanel(
    onScanEncrypted: () -> Unit,
    scanSubmitted: Boolean,
    snackbars: SnackbarHostState,
    modifier: Modifier = Modifier,
) {
    var tab by rememberSaveable { mutableStateOf(0) }

    Column(modifier.fillMaxSize()) {
        PrimaryTabRow(selectedTabIndex = tab) {
            Tab(
                selected = tab == 0,
                onClick = { tab = 0 },
                text = { Text(stringResource(R.string.devices_link_tab_standard)) },
            )
            Tab(
                selected = tab == 1,
                onClick = { tab = 1 },
                text = { Text(stringResource(R.string.devices_link_tab_encrypted)) },
            )
        }

        Column(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Spacer(Modifier.height(16.dp))
            if (tab == 0) StandardLinkTab(snackbars)
            else EncryptedLinkTab(onScanEncrypted, scanSubmitted)
            Spacer(Modifier.height(24.dp))
        }
    }
}

@Composable
private fun StandardLinkTab(snackbars: SnackbarHostState) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val devices by ChatRepository.devices.collectAsStateWithLifecycle()

    // Held so the "a device was paired" confirmation only reacts to devices that
    // appear while this panel is on screen.
    val known = remember { devices.map { it.id }.toSet() }
    val paired = devices.any { it.id !in known }

    // newSyncString() rotates the stored secret, so it is requested exactly once
    // per visit; an old code stops working the moment a new one is issued.
    val code by produceState<String?>(null) {
        value = runCatching { EngineHolder.client?.newSyncString() }.getOrNull()
    }

    val offer = code
    if (offer == null) {
        CircularProgressIndicator(Modifier.padding(vertical = 48.dp))
        return
    }

    Text(
        text = stringResource(R.string.devices_link_instructions),
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
    )
    Spacer(Modifier.height(20.dp))

    QrCodeImage(content = offer, modifier = Modifier.size(280.dp))

    Spacer(Modifier.height(20.dp))
    OutlinedTextField(
        value = offer,
        onValueChange = {},
        readOnly = true,
        maxLines = 3,
        label = { Text(stringResource(R.string.devices_link_code_label)) },
        trailingIcon = {
            IconButton(
                onClick = {
                    context.copyToClipboard(context.getString(R.string.devices_link_title), offer)
                    scope.launch {
                        snackbars.showSnackbar(context.getString(R.string.copied_to_clipboard))
                    }
                }
            ) {
                Icon(
                    Icons.Filled.ContentCopy,
                    contentDescription = stringResource(R.string.action_copy),
                )
            }
        },
        modifier = Modifier.fillMaxWidth(),
    )

    if (paired) {
        Spacer(Modifier.height(16.dp))
        Text(
            text = stringResource(R.string.devices_added),
            style = MaterialTheme.typography.bodyMedium,
            textAlign = TextAlign.Center,
        )
    }
}

@Composable
private fun EncryptedLinkTab(onScan: () -> Unit, scanSubmitted: Boolean) {
    val devices by ChatRepository.devices.collectAsStateWithLifecycle()

    // Saveable for the same reason the panel itself is: the scanner destination
    // replaces this screen, and the accept/reject lands after it is dismissed.
    var deviceCountAtScan by rememberSaveable { mutableStateOf(-1) }
    var rejected by rememberSaveable { mutableStateOf(false) }

    LaunchedEffect(Unit) {
        ChatRepository.effects.collect { effect ->
            if (effect is RepositoryEffect.EncryptedDeviceRejected) rejected = true
        }
    }

    val scanned = scanSubmitted && deviceCountAtScan >= 0
    val added = scanned && devices.size > deviceCountAtScan

    Text(
        text = stringResource(R.string.devices_encrypted_explainer),
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
    )
    Spacer(Modifier.height(20.dp))
    Button(
        onClick = {
            deviceCountAtScan = devices.size
            rejected = false
            onScan()
        },
        modifier = Modifier.fillMaxWidth(),
    ) {
        Text(stringResource(R.string.devices_encrypted_scan))
    }

    if (scanned) {
        Spacer(Modifier.height(20.dp))
        when {
            added -> Text(
                text = stringResource(R.string.devices_added),
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Center,
            )

            rejected -> Text(
                text = stringResource(R.string.devices_encrypted_failed),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.error,
                textAlign = TextAlign.Center,
            )

            else -> Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.Center,
            ) {
                CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                Text(
                    text = stringResource(R.string.devices_encrypted_sending),
                    style = MaterialTheme.typography.bodyMedium,
                    modifier = Modifier.padding(start = 12.dp),
                )
            }
        }
    }
}

private fun Context.copyToClipboard(label: String, text: String) {
    val manager = getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager ?: return
    manager.setPrimaryClip(ClipData.newPlainText(label, text))
}

private const val MAX_DEVICE_NAME_LENGTH = 128
