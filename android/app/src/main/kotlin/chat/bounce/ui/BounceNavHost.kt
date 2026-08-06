package chat.bounce.ui

import android.net.Uri
import android.util.Log
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogProperties
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.dialog
import androidx.navigation.navArgument
import chat.bounce.BounceApplication
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.data.awaitNetworkOnline
import chat.bounce.engine.EngineClient
import chat.bounce.engine.EngineHolder
import chat.bounce.ui.contacts.AddContactScreen
import chat.bounce.ui.contacts.ContactsScreen
import chat.bounce.ui.conversation.ConversationScreen
import chat.bounce.ui.details.ContactProfileScreen
import chat.bounce.ui.details.GroupInfoScreen
import chat.bounce.ui.devices.DevicesScreen
import chat.bounce.ui.groups.NewGroupScreen
import chat.bounce.ui.onboarding.OnboardingScreen
import chat.bounce.ui.qr.QrScannerScreen
import chat.bounce.ui.settings.SettingsScreen
import chat.bounce.ui.threads.NewChatSheet
import chat.bounce.ui.threads.ThreadListScreen
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * Every destination in the app.
 *
 * The Fyne client kept a hand-rolled `viewStack` of int view types; here the
 * back stack is Navigation's, and the thread UUID that used to live in
 * `view.context` is a route argument.
 */
object Routes {
    const val ONBOARDING = "onboarding"
    const val THREADS = "threads"
    const val CONVERSATION = "conversation/{threadId}"
    const val NEW_CHAT = "newChat"
    const val NEW_GROUP = "newGroup"
    const val CONTACTS = "contacts"
    const val ADD_CONTACT = "addContact"
    const val LINK_DEVICE = "linkDevice"
    const val DEVICES = "devices"
    const val SETTINGS = "settings"
    const val SCAN_QR = "scanQr?title={title}"

    // A thread's info screen is two destinations, not one: a group and a DM have
    // nothing in common beyond being reachable from the same toolbar button.
    const val GROUP_INFO = "groupInfo/{groupId}"
    const val CONTACT_PROFILE = "contactProfile/{userId}"

    const val ARG_THREAD_ID = "threadId"
    const val ARG_TITLE = "title"
    const val ARG_GROUP_ID = "groupId"
    const val ARG_USER_ID = "userId"

    fun conversation(threadId: String): String = "conversation/${Uri.encode(threadId)}"

    fun scanQr(title: String): String = "scanQr?title=${Uri.encode(title)}"

    fun groupInfo(groupId: String): String = "groupInfo/${Uri.encode(groupId)}"

    fun contactProfile(userId: String): String = "contactProfile/${Uri.encode(userId)}"
}

/**
 * Set on the launching destination's savedStateHandle when the scanner reads a
 * code, so a screen can distinguish "the user scanned something" from "the user
 * opened the camera and backed out".
 */
const val RESULT_SCAN_SUBMITTED = "scan_submitted"

/**
 * @param deepLinkThreadId a thread UUID from a notification tap or a
 *   conversation shortcut, or null. Consumed via [onDeepLinkHandled] once
 *   navigated, so a configuration change does not re-open the thread.
 */
@Composable
fun BounceNavHost(
    navController: NavHostController,
    deepLinkThreadId: String?,
    onDeepLinkHandled: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val profile by ChatRepository.profile.collectAsStateWithLifecycle()
    val ready by rememberEngineReady(hasProfile = profile != null)

    // Before the engine has handed over its initial state, "no profile" and
    // "not read yet" look identical, and guessing wrong flashes the first-run
    // flow at an existing user on every cold start.
    if (!ready) {
        LoadingScreen(modifier)
        return
    }

    // A device that finishes pairing receives its profile over sync rather than
    // from the setup flow, so leaving onboarding cannot depend on onDone alone.
    LaunchedEffect(profile != null) {
        if (profile != null && navController.currentDestination?.route == Routes.ONBOARDING) {
            navController.navigate(Routes.THREADS) {
                popUpTo(Routes.ONBOARDING) { inclusive = true }
            }
        }
    }

    LaunchedEffect(deepLinkThreadId, profile != null) {
        val threadId = deepLinkThreadId ?: return@LaunchedEffect
        if (profile == null) return@LaunchedEffect

        // Matches the Fyne behaviour of resetting the stack to [inbox, thread]:
        // Back out of a notification-opened chat lands on the inbox, not on
        // whatever the user happened to be looking at hours ago.
        navController.navigate(Routes.conversation(threadId)) {
            popUpTo(Routes.THREADS)
            launchSingleTop = true
        }
        onDeepLinkHandled()
    }

    NavHost(
        navController = navController,
        startDestination = if (profile == null) Routes.ONBOARDING else Routes.THREADS,
        modifier = modifier,
    ) {
        composable(Routes.ONBOARDING) {
            OnboardingScreen(
                onDone = {
                    navController.navigate(Routes.THREADS) {
                        popUpTo(Routes.ONBOARDING) { inclusive = true }
                    }
                },
            )
        }

        composable(Routes.THREADS) {
            ThreadListScreen(
                onOpenThread = { navController.navigate(Routes.conversation(it)) },
                onNewChat = { navController.navigate(Routes.NEW_CHAT) },
                onNewGroup = { navController.navigate(Routes.NEW_GROUP) },
                onOpenContacts = { navController.navigate(Routes.CONTACTS) },
                onAddContact = { navController.navigate(Routes.ADD_CONTACT) },
                onOpenDevices = { navController.navigate(Routes.DEVICES) },
                onOpenSettings = { navController.navigate(Routes.SETTINGS) },
            )
        }

        composable(
            route = Routes.CONVERSATION,
            arguments = listOf(navArgument(Routes.ARG_THREAD_ID) { type = NavType.StringType }),
        ) { entry ->
            ConversationScreen(
                threadId = entry.arguments?.getString(Routes.ARG_THREAD_ID).orEmpty(),
                onBack = { navController.popBackStack() },
                // A thread ID is either a group UUID or the other participant's
                // user UUID, and only the repository knows which.
                onOpenThreadInfo = { threadId ->
                    navController.navigate(
                        if (ChatRepository.isGroup(threadId)) Routes.groupInfo(threadId)
                        else Routes.contactProfile(threadId)
                    )
                },
                onOpenUserProfile = { navController.navigate(Routes.contactProfile(it)) },
                // Replaces the group rather than stacking on it, the same way a
                // notification tap does above: one conversation is not a
                // sub-screen of another, and Back from the DM landing on the
                // inbox keeps the engine's active thread honest - the group's
                // entry is gone, so nothing is on screen claiming to be open
                // that the engine no longer thinks is.
                onOpenDirectMessage = { userId ->
                    navController.navigate(Routes.conversation(userId)) {
                        popUpTo(Routes.THREADS)
                        launchSingleTop = true
                    }
                },
            )
        }

        composable(
            route = Routes.GROUP_INFO,
            arguments = listOf(navArgument(Routes.ARG_GROUP_ID) { type = NavType.StringType }),
        ) { entry ->
            GroupInfoScreen(
                groupId = entry.arguments?.getString(Routes.ARG_GROUP_ID).orEmpty(),
                onBack = { navController.popBackStack() },
                onOpenContact = { navController.navigate(Routes.contactProfile(it)) },
            )
        }

        composable(
            route = Routes.CONTACT_PROFILE,
            arguments = listOf(navArgument(Routes.ARG_USER_ID) { type = NavType.StringType }),
        ) { entry ->
            ContactProfileScreen(
                userId = entry.arguments?.getString(Routes.ARG_USER_ID).orEmpty(),
                onBack = { navController.popBackStack() },
                onOpenGroup = { navController.navigate(Routes.groupInfo(it)) },
                // The profile and the conversation can each reach the other, so
                // without resetting to [inbox, thread] the back stack grows a
                // chat/profile/chat chain the user has to unwind one at a time.
                onMessage = { threadId ->
                    navController.navigate(Routes.conversation(threadId)) {
                        popUpTo(Routes.THREADS)
                        launchSingleTop = true
                    }
                },
            )
        }

        dialog(
            route = Routes.NEW_CHAT,
            // The sheet draws its own rounded surface against the bottom edge.
            dialogProperties = DialogProperties(usePlatformDefaultWidth = false),
        ) {
            NewChatSheet(
                onOpenThread = { navController.navigate(Routes.conversation(it)) },
                onNewGroup = { navController.navigate(Routes.NEW_GROUP) },
                onAddContact = { navController.navigate(Routes.ADD_CONTACT) },
                onDismiss = { navController.popBackStack(Routes.NEW_CHAT, inclusive = true) },
            )
        }

        composable(Routes.NEW_GROUP) {
            NewGroupScreen(
                onBack = { navController.popBackStack() },
                onCreated = { groupId ->
                    navController.navigate(Routes.conversation(groupId)) {
                        popUpTo(Routes.NEW_GROUP) { inclusive = true }
                    }
                },
            )
        }

        // The address book, which is a superset of the inbox: a paired contact
        // has no thread until someone says something.
        composable(Routes.CONTACTS) {
            ContactsScreen(
                onBack = { navController.popBackStack() },
                onOpenThread = { navController.navigate(Routes.conversation(it)) },
                onOpenProfile = { navController.navigate(Routes.contactProfile(it)) },
                onAddContact = { navController.navigate(Routes.ADD_CONTACT) },
            )
        }

        composable(Routes.ADD_CONTACT) { entry ->
            val title = stringResource(R.string.add_contact)
            // Whether the camera actually produced a code. Opening the scanner
            // is not the same event as scanning something, and the screen has no
            // other way to tell the difference once it has been disposed and
            // recreated around the camera destination.
            val scanned by entry.savedStateHandle
                .getStateFlow(RESULT_SCAN_SUBMITTED, false)
                .collectAsStateWithLifecycle()
            AddContactScreen(
                onBack = { navController.popBackStack() },
                onScan = {
                    // Cleared on the way in, so a second attempt does not
                    // inherit the previous scan's outcome.
                    entry.savedStateHandle[RESULT_SCAN_SUBMITTED] = false
                    navController.navigate(Routes.scanQr(title))
                },
                scanSubmitted = scanned,
            )
        }

        composable(Routes.DEVICES) { entry ->
            val scanned by entry.savedStateHandle
                .getStateFlow(RESULT_SCAN_SUBMITTED, false)
                .collectAsStateWithLifecycle()
            DevicesScreen(
                onBack = { navController.popBackStack() },
                onLinkDevice = {
                    entry.savedStateHandle[RESULT_SCAN_SUBMITTED] = false
                    navController.navigate(Routes.LINK_DEVICE)
                },
                scanSubmitted = scanned,
            )
        }

        composable(Routes.SETTINGS) {
            SettingsScreen(onBack = { navController.popBackStack() })
        }

        // The two scanners differ only in which engine call consumes the code.
        // Neither AddContactScreen nor DevicesScreen takes a result: both watch
        // the repository for the outcome instead, because the handshake succeeds
        // or fails long after the camera closes.
        composable(
            route = Routes.SCAN_QR,
            arguments = listOf(
                navArgument(Routes.ARG_TITLE) {
                    type = NavType.StringType
                    defaultValue = ""
                },
            ),
        ) { entry ->
            val fallback = stringResource(R.string.scan_qr_title)
            QrScannerScreen(
                title = entry.arguments?.getString(Routes.ARG_TITLE)?.ifBlank { fallback } ?: fallback,
                onResult = { code ->
                    navController.previousBackStackEntry
                        ?.savedStateHandle?.set(RESULT_SCAN_SUBMITTED, true)
                    navController.popBackStack()
                    submitScannedCode("add user") { it.requestToAddUser(code) }
                },
                onBack = { navController.popBackStack() },
            )
        }

        composable(Routes.LINK_DEVICE) {
            QrScannerScreen(
                title = stringResource(R.string.link_device),
                onResult = { code ->
                    navController.previousBackStackEntry
                        ?.savedStateHandle?.set(RESULT_SCAN_SUBMITTED, true)
                    navController.popBackStack()
                    submitScannedCode("manage encrypted device") {
                        it.requestToManageEncryptedDevice(code)
                    }
                },
                onBack = { navController.popBackStack() },
            )
        }
    }
}

/**
 * Hands a scanned pairing code to the engine.
 *
 * Deliberately on the application scope rather than a composition one: the
 * scanner is popped the instant the code is read, and a request cancelled with
 * it would leave the peer waiting on a handshake that never arrives.
 */
private fun submitScannedCode(what: String, block: suspend (EngineClient) -> Unit) {
    val client = EngineHolder.client
    if (client == null) {
        Log.w(TAG, "engine not started, dropping scanned code for $what")
        return
    }
    BounceApplication.scope.launch {
        // Both of these dial the peer, and dialing before Tor has finished
        // bootstrapping fails instantly rather than waiting - see
        // awaitNetworkOnline. Holding the request costs the user nothing here
        // because the scanner has already been popped.
        if (!awaitNetworkOnline()) {
            Log.w(TAG, "network never came online, dropping scanned code for $what")
            return@launch
        }
        runCatching { block(client) }.onFailure { Log.w(TAG, "$what request failed", it) }
    }
}

/**
 * Whether the app knows enough to choose between onboarding and the inbox.
 *
 * Waits for the engine's snapshot to be *installed*, not for a timeout. The
 * previous version polled EngineHolder.client and then allowed a fixed grace
 * period for a profile to turn up, which was wrong twice over: the client goes
 * non-null when the engine object is constructed, before GetInitialState has
 * been called at all, and the grace period then has to outlast that whole call.
 * It did not - GetInitialState has been measured on real hardware at 1490ms and
 * 1565ms against a 1500ms grace - so a cold start could fall through with no
 * profile loaded yet and open the first-run flow on an existing account, which
 * is the flash of setup that gets seen and then immediately corrected.
 *
 * There is no timeout now, because there is no safe thing to do on expiry:
 * guessing "no profile" is precisely the bug. If the engine never loads, the
 * loading screen stays up, which is honest - the service logs the failure and
 * moves to Phase.OFFLINE.
 */
@Composable
private fun rememberEngineReady(hasProfile: Boolean) = produceState(
    initialValue = false,
    key1 = hasProfile,
) {
    // Already known - a profile arriving later (a device finishing pairing) must
    // not drop the UI back to loading.
    if (hasProfile) {
        value = true
        return@produceState
    }
    ChatRepository.initialStateLoaded.first { it }
    value = true
}

/** The engine opens a database and starts Tor before it can answer anything. */
@Composable
private fun LoadingScreen(modifier: Modifier = Modifier) {
    Surface(modifier = modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(
            modifier = Modifier.fillMaxSize(),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Image(
                painter = painterResource(R.drawable.ic_bounce_logo),
                contentDescription = stringResource(R.string.app_name),
                modifier = Modifier.size(96.dp),
            )
            Spacer(Modifier.height(32.dp))
            CircularProgressIndicator(modifier = Modifier.width(36.dp))
        }
    }
}

private const val TAG = "BounceNav"
