package chat.bounce.ui.contacts

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
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
import chat.bounce.engine.EngineHolder
import chat.bounce.ui.qr.QrCodeImage
import kotlinx.coroutines.launch

/**
 * Add a contact by exchanging a one-shot pairing code.
 *
 * There is no directory to look anyone up in - a Bounce identity is an onion
 * address, and the only way to learn one is for the other device to hand it
 * over. That is why this screen is symmetric: one side shows, the other scans,
 * and both have to be running at the same moment for the handshake to land.
 *
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AddContactScreen(
    onBack: () -> Unit,
    onScan: () -> Unit,
    /** True once the scanner has actually read a code, not merely been opened. */
    scanSubmitted: Boolean = false,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val snackbars = remember { SnackbarHostState() }

    var tab by rememberSaveable { mutableStateOf(0) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.add_contact)) },
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
        snackbarHost = { SnackbarHost(snackbars) },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize(),
        ) {
            PrimaryTabRow(selectedTabIndex = tab) {
                Tab(
                    selected = tab == 0,
                    onClick = { tab = 0 },
                    text = { Text(stringResource(R.string.add_contact_tab_my_code)) },
                )
                Tab(
                    selected = tab == 1,
                    onClick = { tab = 1 },
                    text = { Text(stringResource(R.string.add_contact_tab_scan)) },
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
                if (tab == 0) {
                    MyCodeTab(
                        onCopied = {
                            scope.launch {
                                snackbars.showSnackbar(
                                    context.getString(R.string.copied_to_clipboard)
                                )
                            }
                        },
                    )
                } else {
                    ScanTab(onScan = onScan, scanSubmitted = scanSubmitted)
                }
                Spacer(Modifier.height(24.dp))
            }
        }
    }
}

@Composable
private fun MyCodeTab(onCopied: () -> Unit) {
    val context = LocalContext.current

    // Every call to newAddUserString() invalidates the previous secret, so it is
    // fetched once per visit to this screen and never on recomposition.
    val code by produceState<String?>(null) {
        value = runCatching { EngineHolder.client?.newAddUserString() }.getOrNull()
    }

    val offer = code
    if (offer == null) {
        CircularProgressIndicator(Modifier.padding(vertical = 48.dp))
        return
    }

    Text(
        text = stringResource(R.string.add_contact_share_instructions),
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
        singleLine = false,
        maxLines = 3,
        label = { Text(stringResource(R.string.add_contact_code_label)) },
        trailingIcon = {
            IconButton(
                onClick = {
                    context.copyToClipboard(context.getString(R.string.add_contact), offer)
                    onCopied()
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

    Spacer(Modifier.height(16.dp))
    Text(
        text = stringResource(R.string.add_contact_both_online),
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
    )
}

@Composable
private fun ScanTab(onScan: () -> Unit, scanSubmitted: Boolean) {
    val users by ChatRepository.users.collectAsStateWithLifecycle()

    // Both survive the round trip through the scanner destination, which
    // disposes this screen: the handshake almost always lands *after* the camera
    // is closed, so the outcome has to be recognisable on the way back.
    // Joined rather than a Set because only primitives and Strings are saveable;
    // UUIDs never contain a comma.
    var knownUserIds by rememberSaveable { mutableStateOf<String?>(null) }
    var rejected by rememberSaveable { mutableStateOf(false) }

    LaunchedEffect(Unit) {
        ChatRepository.effects.collect { effect ->
            if (effect is RepositoryEffect.AddUserRejected) rejected = true
        }
    }

    // There is no engine callback tying a completion to the offer we sent, so
    // success is read the way the rest of the UI reads it: a contact that was
    // not in the list when we opened the camera now is.
    val addedName = knownUserIds?.let { known ->
        val before = known.split(',').toSet()
        users.values.firstOrNull { it.id !in before }?.displayName
    }

    Text(
        text = stringResource(R.string.add_contact_scan_instructions),
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
    )
    Spacer(Modifier.height(20.dp))

    Button(
        onClick = {
            knownUserIds = users.keys.joinToString(",")
            rejected = false
            onScan()
        },
        modifier = Modifier.fillMaxWidth(),
    ) {
        Text(stringResource(R.string.add_contact_open_camera))
    }

    Spacer(Modifier.height(16.dp))
    Text(
        text = stringResource(R.string.add_contact_both_online),
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
    )

    if (scanSubmitted && knownUserIds != null) {
        Spacer(Modifier.height(24.dp))
        when {
            addedName != null -> Text(
                text = stringResource(R.string.add_contact_added, addedName),
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Center,
            )

            rejected -> Text(
                text = stringResource(R.string.add_contact_rejected),
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
                    text = stringResource(R.string.add_contact_sending),
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
