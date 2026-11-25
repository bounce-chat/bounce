package chat.bounce.ui.onboarding

import android.os.Build
import android.util.Log
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Devices
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.data.InitialSyncState
import chat.bounce.data.RepositoryEffect
import chat.bounce.data.awaitNetworkOnline
import chat.bounce.engine.EngineClient
import chat.bounce.engine.EngineHolder
import chat.bounce.ui.components.AvatarPickerButton
import chat.bounce.ui.components.rememberAvatarPicker
import chat.bounce.ui.qr.QrScannerScreen
import chat.bounce.ui.theme.isDarkTheme
import kotlinx.coroutines.launch

/**
 * First-run setup: create a profile, or attach this device to one that already
 * exists.
 *
 * Bounce has no account server, so "sign in" does not exist as a concept. The
 * only way onto an existing profile is to hold this device up to one that is
 * already signed in, which is why the second path is a QR scan rather than a
 * credential form.
 */
@Composable
fun OnboardingScreen(onDone: () -> Unit) {
    val context = LocalContext.current

    // start() is idempotent and mutex-guarded, so if BounceService already has
    // one in flight this simply waits on it rather than starting a second.
    val client by produceState<EngineClient?>(null) {
        value = runCatching { EngineHolder.start(context) }.getOrNull()
    }

    var step by rememberSaveable { mutableStateOf(OnboardingStep.Welcome) }

    // The system back button walks the steps the way their arrows do. The
    // progress step is not here: leaving it has to give the request up, which
    // LinkDeviceFlow does.
    BackHandler(
        enabled = step == OnboardingStep.CreateProfile ||
            step == OnboardingStep.LinkIntro ||
            step == OnboardingStep.LinkScan,
    ) {
        step = if (step == OnboardingStep.LinkScan) OnboardingStep.LinkIntro else OnboardingStep.Welcome
    }

    when (step) {
        OnboardingStep.Welcome -> WelcomeStep(
            engineStarting = client == null,
            onCreate = { step = OnboardingStep.CreateProfile },
            onLink = { step = OnboardingStep.LinkIntro },
        )

        OnboardingStep.CreateProfile -> CreateProfileStep(
            client = client,
            onBack = { step = OnboardingStep.Welcome },
            onDone = onDone,
        )

        OnboardingStep.LinkIntro,
        OnboardingStep.LinkScan,
        OnboardingStep.LinkProgress,
        -> LinkDeviceFlow(
            client = client,
            step = step,
            onStepChange = { step = it },
            onDone = onDone,
        )
    }
}

private enum class OnboardingStep { Welcome, CreateProfile, LinkIntro, LinkScan, LinkProgress }

// --- welcome ----------------------------------------------------------------

@Composable
private fun WelcomeStep(
    engineStarting: Boolean,
    onCreate: () -> Unit,
    onLink: () -> Unit,
) {
    // The Surface is what makes this step follow the app's theme. Without it the
    // background is the window's, which follows the system even when the theme
    // has been set the other way in the app, and text with no colour of its own
    // is black on either.
    Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Spacer(Modifier.height(72.dp))
            Image(
                painter = painterResource(
                    if (isDarkTheme()) R.drawable.ic_bounce_wordmark_dark else R.drawable.ic_bounce_wordmark,
                ),
                contentDescription = stringResource(R.string.app_name),
                modifier = Modifier.height(64.dp),
            )

            Spacer(Modifier.height(56.dp))

            SetupCard(
                title = stringResource(R.string.onboarding_create_profile),
                subtitle = stringResource(R.string.onboarding_create_profile_subtitle),
                icon = { Icon(Icons.Filled.PersonAdd, contentDescription = null) },
                onClick = onCreate,
            )
            Spacer(Modifier.height(12.dp))
            SetupCard(
                title = stringResource(R.string.onboarding_link_device),
                subtitle = stringResource(R.string.onboarding_link_device_subtitle),
                icon = { Icon(Icons.Filled.Devices, contentDescription = null) },
                onClick = onLink,
            )

            Spacer(Modifier.height(24.dp))
            if (engineStarting) {
                // The engine may still be bootstrapping Tor when this screen paints.
                // Saying so is better than a button that silently does nothing.
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                    Text(
                        text = stringResource(R.string.onboarding_starting),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(start = 12.dp),
                    )
                }
            }
            Spacer(Modifier.height(32.dp))
        }
    }
}

@Composable
private fun SetupCard(
    title: String,
    subtitle: String,
    icon: @Composable () -> Unit,
    onClick: () -> Unit,
) {
    OutlinedCard(
        modifier = Modifier.fillMaxWidth().clickable(onClick = onClick),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(20.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            icon()
            Column(Modifier.padding(start = 16.dp)) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(2.dp))
                Text(
                    text = subtitle,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

// --- create a new profile ---------------------------------------------------

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CreateProfileStep(
    client: EngineClient?,
    onBack: () -> Unit,
    onDone: () -> Unit,
) {
    val scope = rememberCoroutineScope()

    var name by rememberSaveable { mutableStateOf("") }
    var deviceName by rememberSaveable { mutableStateOf(Build.MODEL.orEmpty()) }
    var avatar by remember { mutableStateOf<ByteArray?>(null) }
    var saving by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf(false) }

    val pickAvatar = rememberAvatarPicker { bytes -> if (bytes != null) avatar = bytes }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.onboarding_create_profile)) },
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
                .padding(horizontal = 24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Spacer(Modifier.height(24.dp))

            AvatarPickerButton(
                picked = avatar,
                size = 128.dp,
                contentDescription = stringResource(R.string.onboarding_add_photo),
                onClick = pickAvatar,
            ) {
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .background(MaterialTheme.colorScheme.surfaceVariant, CircleShape),
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(
                        imageVector = Icons.Filled.Person,
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(56.dp),
                    )
                }
            }
            Spacer(Modifier.height(8.dp))
            TextButton(onClick = pickAvatar) {
                Text(
                    stringResource(
                        if (avatar == null) R.string.onboarding_add_photo
                        else R.string.onboarding_change_photo
                    )
                )
            }

            Spacer(Modifier.height(16.dp))
            OutlinedTextField(
                value = name,
                // The engine caps names at 128 characters; trimming the leading
                // space keeps the derived initials from being blank.
                onValueChange = { name = it.trimStart().take(MAX_NAME_LENGTH) },
                label = { Text(stringResource(R.string.onboarding_your_name)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
                modifier = Modifier.fillMaxWidth(),
            )

            Spacer(Modifier.height(16.dp))
            OutlinedTextField(
                value = deviceName,
                onValueChange = { deviceName = it.trimStart().take(MAX_NAME_LENGTH) },
                label = { Text(stringResource(R.string.onboarding_device_name)) },
                supportingText = { Text(stringResource(R.string.onboarding_device_name_help)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                modifier = Modifier.fillMaxWidth(),
            )

            if (failed) {
                Spacer(Modifier.height(12.dp))
                Text(
                    text = stringResource(R.string.onboarding_profile_failed),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }

            Spacer(Modifier.height(24.dp))
            Button(
                enabled = client != null && name.isNotBlank() && !saving,
                onClick = {
                    val engine = client ?: return@Button
                    saving = true
                    failed = false
                    scope.launch {
                        val result = runCatching {
                            engine.setProfile(
                                name.trim(),
                                avatar ?: ByteArray(0),
                                deviceName.trim().ifBlank { DEFAULT_DEVICE_NAME },
                            )
                        }
                        saving = false
                        if (result.isSuccess) onDone() else failed = true
                    }
                },
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(stringResource(R.string.onboarding_create))
            }

            if (client == null) {
                Spacer(Modifier.height(12.dp))
                Text(
                    text = stringResource(R.string.onboarding_starting),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.height(32.dp))
        }
    }
}

// --- link this device to an existing profile --------------------------------

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun LinkDeviceFlow(
    client: EngineClient?,
    step: OnboardingStep,
    onStepChange: (OnboardingStep) -> Unit,
    onDone: () -> Unit,
) {
    val scope = rememberCoroutineScope()

    var deviceName by rememberSaveable { mutableStateOf(Build.MODEL.orEmpty()) }
    var requestFailed by remember { mutableStateOf(false) }
    /** The engine's own message, shown beneath the generic one so a failure is diagnosable. */
    var requestError by remember { mutableStateOf<String?>(null) }
    var rejected by remember { mutableStateOf(false) }
    /** The other device answered, but with a profile the engine would not take. */
    var refused by remember { mutableStateOf(false) }

    // Null until the peer answers; false means it accepted but had no history to
    // send, in which case no InitialSync* event will ever arrive.
    var importExpected by remember { mutableStateOf<Boolean?>(null) }

    // Counts the requests made and given up. What a request does once it comes
    // back from waiting, for the network or inside the engine, is only acted on
    // if the count is still what it was when the request began.
    var attempt by remember { mutableIntStateOf(0) }

    // A request can be given up until the other device's answer is accepted.
    // After that both devices have each other and the import is running, which
    // is not something to back out of.
    val cancellable = importExpected == null
    val cancelRequest: () -> Unit = {
        attempt += 1
        // Leave first: the engine call below can take a moment, and nothing
        // visible should wait on it.
        onStepChange(OnboardingStep.LinkIntro)
        scope.launch {
            val engine = client ?: EngineHolder.client
            val cancelled = engine == null || runCatching { engine.cancelSyncRequest() }.getOrDefault(true)
            // The answer was accepted before the cancel got there, so pairing
            // is going ahead and this is the step that shows it.
            if (!cancelled) onStepChange(OnboardingStep.LinkProgress)
        }
    }

    val networkOnline by ChatRepository.networkOnline.collectAsStateWithLifecycle()
    val sync by ChatRepository.initialSync.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) {
        ChatRepository.effects.collect { effect ->
            when (effect) {
                is RepositoryEffect.SyncDeviceRejected -> rejected = true
                is RepositoryEffect.SyncDeviceProfileRefused -> {
                    requestError = effect.reason
                    refused = true
                }
                is RepositoryEffect.SyncDeviceAccepted -> {
                    importExpected = effect.references
                    // Wherever the user has got to: an answer can be accepted
                    // in the instant they back out, and from here on the
                    // progress step is the only one that leads anywhere.
                    onStepChange(OnboardingStep.LinkProgress)
                }
                else -> Unit
            }
        }
    }

    LaunchedEffect(sync, importExpected, step) {
        if (step != OnboardingStep.LinkProgress) return@LaunchedEffect
        val done = sync is InitialSyncState.Complete || importExpected == false
        if (done) {
            applyDeviceName(deviceName)
            onDone()
        }
    }

    BackHandler(enabled = step == OnboardingStep.LinkProgress && cancellable, onBack = cancelRequest)

    when (step) {
        OnboardingStep.LinkScan -> QrScannerScreen(
            title = stringResource(R.string.onboarding_link_device),
            onResult = { code ->
                onStepChange(OnboardingStep.LinkProgress)
                requestFailed = false
                requestError = null
                rejected = false
                refused = false
                importExpected = null
                attempt += 1
                val mine = attempt
                scope.launch {
                    val engine = client ?: EngineHolder.client
                    if (engine == null) {
                        Log.w(TAG, "engine not started, cannot send pairing request")
                        requestFailed = true
                        return@launch
                    }
                    // Holding the request rather than sending it immediately:
                    // Tor is still bootstrapping for the first minute or so
                    // after install, and dialing during that window blocks
                    // inside the engine call, which cannot be cancelled. The
                    // progress screen already renders a "waiting for the
                    // network" state for exactly this.
                    val online = awaitNetworkOnline()
                    // Given up while the network was coming up: nothing has
                    // been sent, and nothing is.
                    if (mine != attempt) return@launch
                    if (!online) {
                        Log.w(TAG, "network never came online, abandoning pairing request")
                        requestFailed = true
                        return@launch
                    }
                    runCatching { engine.requestToSync(code) }.onFailure {
                        // A request given up while it was dialling comes back
                        // as a failure, and is not one the user is waiting on.
                        if (mine != attempt) return@onFailure
                        Log.w(TAG, "requestToSync failed", it)
                        requestError = it.message
                        requestFailed = true
                    }
                }
            },
            onBack = { onStepChange(OnboardingStep.LinkIntro) },
        )

        OnboardingStep.LinkProgress -> Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text(stringResource(R.string.onboarding_link_device)) },
                    navigationIcon = {
                        if (cancellable) {
                            IconButton(onClick = cancelRequest) {
                                Icon(
                                    Icons.AutoMirrored.Filled.ArrowBack,
                                    contentDescription = stringResource(R.string.action_back),
                                )
                            }
                        }
                    },
                )
            },
        ) { padding ->
            SyncProgressBody(
                modifier = Modifier.padding(padding),
                accepted = importExpected != null,
                sync = sync,
                networkOnline = networkOnline,
                rejected = rejected,
                refused = refused,
                requestFailed = requestFailed,
                requestError = requestError,
                onRetry = { onStepChange(OnboardingStep.LinkIntro) },
            )
        }

        else -> Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text(stringResource(R.string.onboarding_link_device)) },
                    navigationIcon = {
                        IconButton(onClick = { onStepChange(OnboardingStep.Welcome) }) {
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
                    .padding(horizontal = 24.dp),
            ) {
                Spacer(Modifier.height(16.dp))
                Text(
                    text = stringResource(R.string.onboarding_link_instructions_title),
                    style = MaterialTheme.typography.titleMedium,
                )
                Spacer(Modifier.height(8.dp))
                Text(
                    text = stringResource(R.string.onboarding_link_instructions),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(24.dp))
                OutlinedTextField(
                    value = deviceName,
                    onValueChange = { deviceName = it.trimStart().take(MAX_NAME_LENGTH) },
                    label = { Text(stringResource(R.string.onboarding_device_name)) },
                    supportingText = { Text(stringResource(R.string.onboarding_device_name_help)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(24.dp))
                Button(
                    enabled = client != null,
                    onClick = { onStepChange(OnboardingStep.LinkScan) },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(stringResource(R.string.onboarding_scan_code))
                }
                if (client == null) {
                    Spacer(Modifier.height(12.dp))
                    Text(
                        text = stringResource(R.string.onboarding_starting),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Spacer(Modifier.height(32.dp))
            }
        }
    }
}

@Composable
private fun SyncProgressBody(
    accepted: Boolean,
    sync: InitialSyncState,
    networkOnline: Boolean,
    rejected: Boolean,
    refused: Boolean,
    requestFailed: Boolean,
    requestError: String?,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        if (rejected || refused || requestFailed) {
            Text(
                text = stringResource(
                    when {
                        rejected -> R.string.onboarding_sync_rejected
                        refused -> R.string.onboarding_sync_refused
                        else -> R.string.onboarding_sync_failed
                    }
                ),
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.error,
                textAlign = TextAlign.Center,
            )
            if (requestError != null) {
                Spacer(Modifier.height(8.dp))
                Text(
                    text = requestError,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                )
            }
            Spacer(Modifier.height(24.dp))
            Button(onClick = onRetry) { Text(stringResource(R.string.onboarding_try_again)) }
            return@Column
        }

        val status = when {
            !networkOnline && !accepted -> stringResource(R.string.onboarding_sync_waiting_network)
            sync is InitialSyncState.Starting -> stringResource(R.string.onboarding_sync_importing)
            sync is InitialSyncState.Progress -> stringResource(R.string.onboarding_sync_importing)
            sync is InitialSyncState.Preparing -> stringResource(R.string.onboarding_sync_preparing)
            accepted -> stringResource(R.string.onboarding_sync_importing)
            else -> stringResource(R.string.onboarding_sync_sending)
        }

        Text(
            text = status,
            style = MaterialTheme.typography.bodyLarge,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(20.dp))

        val fraction = (sync as? InitialSyncState.Progress)?.fraction
        if (fraction != null) {
            LinearProgressIndicator(
                progress = { fraction.toFloat().coerceIn(0f, 1f) },
                modifier = Modifier.fillMaxWidth(),
            )
        } else {
            LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
        }

        Spacer(Modifier.height(20.dp))
        Text(
            text = stringResource(R.string.onboarding_keep_app_open),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
    }
}

/**
 * Applies the name the user typed before pairing started.
 *
 * The local device row only exists once the peer has accepted us and the device
 * list has replicated, so this cannot run any earlier than sync completion.
 */
private suspend fun applyDeviceName(wanted: String) {
    val trimmed = wanted.trim()
    if (trimmed.isBlank()) return
    val local = ChatRepository.devices.value.firstOrNull { it.local } ?: return
    if (trimmed == local.name) return
    runCatching { EngineHolder.client?.renameDevice(local.id, trimmed) }
}

private const val TAG = "BounceOnboarding"
private const val MAX_NAME_LENGTH = 128
private const val DEFAULT_DEVICE_NAME = "Phone"
