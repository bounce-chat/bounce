package chat.bounce.ui.settings

import android.content.ClipData
import android.content.ClipDescription
import android.content.ClipboardManager
import android.content.Context
import android.os.Build
import android.os.PersistableBundle
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogProperties
import chat.bounce.R
import chat.bounce.engine.EngineHolder
import chat.bounce.engine.SecurityStatus
import chat.bounce.goengine.Goengine
import chat.bounce.ui.components.PasswordField
import java.text.DateFormat
import java.util.Date
import kotlinx.coroutines.launch

/**
 * How the install is protected on this device, and the user's side of it: the
 * password and the recovery code.
 *
 * Unlike the rest of the settings, nothing here is echoed back by the engine,
 * so the status is read when the section appears and again after each change.
 */
@Composable
internal fun SecuritySection() {
    var status by remember { mutableStateOf<SecurityStatus?>(null) }
    var changes by remember { mutableIntStateOf(0) }
    LaunchedEffect(changes) {
        status = runCatching { EngineHolder.client?.securityStatus() }.getOrNull()
    }

    var dialog by remember { mutableStateOf<SecurityDialog>(SecurityDialog.None) }

    val current = status
    Column(Modifier.padding(horizontal = 24.dp)) {
        if (current != null) {
            Text(
                text = stringResource(
                    when {
                        current.keystoreUsed && current.passwordSet -> R.string.security_status_keystore_password
                        current.keystoreUsed -> R.string.security_status_keystore
                        current.passwordSet -> R.string.security_status_password
                        else -> R.string.security_status_none
                    },
                ),
                style = MaterialTheme.typography.bodyLarge,
            )
            Spacer(Modifier.height(4.dp))
        }
        Text(
            text = stringResource(R.string.security_help),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )

        if (current != null) {
            Spacer(Modifier.height(12.dp))
            OutlinedButton(
                onClick = { dialog = SecurityDialog.SetPassword(current) },
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(
                    stringResource(
                        if (current.passwordSet) R.string.security_change_password else R.string.security_set_password,
                    ),
                )
            }
            if (current.passwordSet) {
                OutlinedButton(
                    onClick = { dialog = SecurityDialog.RemovePassword(current) },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(stringResource(R.string.security_remove_password))
                }
            }

            Spacer(Modifier.height(16.dp))
            Text(
                text = when {
                    // Nothing protects the install, so there is nothing a code would recover
                    !current.protected -> stringResource(R.string.security_recovery_unavailable)
                    current.recoveryCodeCreatedAt == 0L -> stringResource(R.string.security_recovery_none)
                    else -> stringResource(
                        R.string.security_recovery_created,
                        DateFormat.getDateInstance(DateFormat.MEDIUM)
                            .format(Date(current.recoveryCodeCreatedAt * 1000)),
                    )
                },
                style = MaterialTheme.typography.bodyLarge,
            )
            if (current.protected) {
                Spacer(Modifier.height(4.dp))
                OutlinedButton(
                    onClick = { dialog = SecurityDialog.CreateCode(current) },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(
                        stringResource(
                            if (current.recoveryCodeCreatedAt == 0L) {
                                R.string.security_create_code
                            } else {
                                R.string.security_create_new_code
                            },
                        ),
                    )
                }
            }
        }
        Spacer(Modifier.height(16.dp))
    }

    when (val shown = dialog) {
        SecurityDialog.None -> Unit

        is SecurityDialog.SetPassword -> SetPasswordDialog(
            status = shown.status,
            onDismiss = { dialog = SecurityDialog.None },
            onSet = { password ->
                changes++
                dialog = SecurityDialog.PasswordSet(shown.status, password)
            },
        )

        is SecurityDialog.PasswordSet -> PasswordSetDialog(
            before = shown.before,
            password = shown.password,
            onDismiss = { dialog = SecurityDialog.None },
            onCode = { code ->
                changes++
                dialog = SecurityDialog.ShowCode(code)
            },
        )

        is SecurityDialog.RemovePassword -> RemovePasswordDialog(
            status = shown.status,
            onDismiss = { dialog = SecurityDialog.None },
            onRemoved = {
                changes++
                dialog = SecurityDialog.None
            },
        )

        is SecurityDialog.CreateCode -> CreateCodeDialog(
            status = shown.status,
            onDismiss = { dialog = SecurityDialog.None },
            onCode = { code ->
                changes++
                dialog = SecurityDialog.ShowCode(code)
            },
        )

        is SecurityDialog.ShowCode -> RecoveryCodeDialog(
            code = shown.code,
            onDismiss = { dialog = SecurityDialog.None },
        )
    }
}

/** Each carries the status the user was looking at when they opened it. */
private sealed interface SecurityDialog {
    data object None : SecurityDialog
    data class SetPassword(val status: SecurityStatus) : SecurityDialog

    /** A password has just been set or changed. [before] is how the install was protected until then. */
    data class PasswordSet(val before: SecurityStatus, val password: String) : SecurityDialog
    data class RemovePassword(val status: SecurityStatus) : SecurityDialog
    data class CreateCode(val status: SecurityStatus) : SecurityDialog
    data class ShowCode(val code: String) : SecurityDialog
}

/** What the user is told when a change to how the install is protected was refused. */
private fun securityErrorText(context: Context, failure: Throwable): String = when (failure.message) {
    Goengine.ErrorWrongPassword -> context.getString(R.string.security_error_wrong_password)
    Goengine.ErrorPasswordRequired -> context.getString(R.string.security_error_password_required)
    Goengine.ErrorKeystoreUnavailable -> context.getString(R.string.security_error_keystore_unavailable)
    Goengine.ErrorKeystoreSecretMissing -> context.getString(R.string.security_error_keystore_missing)
    Goengine.ErrorRollUnfinished -> context.getString(R.string.security_error_restart_first)
    else -> context.getString(R.string.security_error_other, failure.message.orEmpty())
}

@Composable
private fun DialogError(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.error,
        modifier = Modifier.padding(top = 12.dp),
    )
}

@Composable
private fun SetPasswordDialog(
    status: SecurityStatus,
    onDismiss: () -> Unit,
    onSet: (String) -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    var current by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var working by remember { mutableStateOf(false) }

    val title = stringResource(
        if (status.passwordSet) R.string.security_change_password else R.string.security_set_password,
    )
    val enterNewPassword = stringResource(R.string.security_enter_new_password)
    val passwordsDiffer = stringResource(R.string.security_passwords_differ)

    AlertDialog(
        onDismissRequest = { if (!working) onDismiss() },
        title = { Text(title) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                Text(
                    text = stringResource(R.string.security_password_consequences),
                    style = MaterialTheme.typography.bodyMedium,
                )
                error?.let { DialogError(it) }
                Spacer(Modifier.height(12.dp))
                if (status.passwordSet) {
                    PasswordField(
                        value = current,
                        onValueChange = { current = it },
                        label = stringResource(R.string.security_current_password),
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Spacer(Modifier.height(8.dp))
                }
                PasswordField(
                    value = password,
                    onValueChange = { password = it },
                    label = stringResource(R.string.security_new_password),
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(8.dp))
                PasswordField(
                    value = confirm,
                    onValueChange = { confirm = it },
                    label = stringResource(R.string.security_confirm_password),
                    imeAction = ImeAction.Done,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = {
            TextButton(
                enabled = !working,
                onClick = {
                    if (password.isEmpty()) {
                        error = enterNewPassword
                        return@TextButton
                    }
                    if (password != confirm) {
                        error = passwordsDiffer
                        return@TextButton
                    }
                    val client = EngineHolder.client ?: return@TextButton
                    val wanted = password
                    error = null
                    working = true
                    scope.launch {
                        val failure = runCatching { client.setPassword(current, wanted) }.exceptionOrNull()
                        working = false
                        if (failure == null) onSet(wanted) else error = securityErrorText(context, failure)
                    }
                },
            ) {
                Text(title)
            }
        },
        dismissButton = {
            TextButton(enabled = !working, onClick = onDismiss) {
                Text(stringResource(R.string.action_cancel))
            }
        },
    )
}

/**
 * Confirms a new password, and offers a recovery code to an install that has
 * none. The password just typed is still in hand, so the code is made without
 * asking for it again.
 */
@Composable
private fun PasswordSetDialog(
    before: SecurityStatus,
    password: String,
    onDismiss: () -> Unit,
    onCode: (String) -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    var error by remember { mutableStateOf<String?>(null) }
    var working by remember { mutableStateOf(false) }

    // A recovery code is untouched by a password change, so what the install
    // had before is what it has now.
    val offerCode = before.recoveryCodeCreatedAt == 0L

    AlertDialog(
        onDismissRequest = { if (!working) onDismiss() },
        title = {
            Text(
                stringResource(
                    if (before.passwordSet) {
                        R.string.security_password_changed_title
                    } else {
                        R.string.security_password_set_title
                    },
                ),
            )
        },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                Text(
                    text = stringResource(
                        when {
                            before.passwordSet -> R.string.security_password_changed
                            // The first protection this install has had, which the engine
                            // finishes at the next start by moving everything to a new key.
                            !before.protected -> R.string.security_password_set_restart
                            else -> R.string.security_password_set
                        },
                    ),
                    style = MaterialTheme.typography.bodyMedium,
                )
                if (offerCode) {
                    Spacer(Modifier.height(12.dp))
                    Text(
                        text = stringResource(R.string.security_code_explanation),
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
                error?.let { DialogError(it) }
            }
        },
        confirmButton = {
            if (offerCode) {
                TextButton(
                    enabled = !working,
                    onClick = {
                        val client = EngineHolder.client ?: return@TextButton
                        error = null
                        working = true
                        scope.launch {
                            val result = runCatching { client.createRecoveryCode(password) }
                            working = false
                            result.fold(
                                onSuccess = onCode,
                                onFailure = { error = securityErrorText(context, it) },
                            )
                        }
                    },
                ) {
                    Text(stringResource(R.string.security_create_code))
                }
            } else {
                TextButton(onClick = onDismiss) {
                    Text(stringResource(R.string.security_done))
                }
            }
        },
        dismissButton = {
            if (offerCode) {
                TextButton(enabled = !working, onClick = onDismiss) {
                    Text(stringResource(R.string.security_not_now))
                }
            }
        },
    )
}

@Composable
private fun RemovePasswordDialog(
    status: SecurityStatus,
    onDismiss: () -> Unit,
    onRemoved: () -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    var current by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var working by remember { mutableStateOf(false) }

    AlertDialog(
        onDismissRequest = { if (!working) onDismiss() },
        title = { Text(stringResource(R.string.security_remove_password)) },
        text = {
            Column {
                Text(
                    text = stringResource(
                        if (status.keystoreUsed) {
                            R.string.security_remove_keeps_keystore
                        } else {
                            R.string.security_remove_unprotected
                        },
                    ),
                    style = MaterialTheme.typography.bodyMedium,
                )
                error?.let { DialogError(it) }
                Spacer(Modifier.height(12.dp))
                PasswordField(
                    value = current,
                    onValueChange = { current = it },
                    label = stringResource(R.string.security_current_password),
                    imeAction = ImeAction.Done,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = {
            TextButton(
                enabled = !working,
                colors = ButtonDefaults.textButtonColors(
                    contentColor = MaterialTheme.colorScheme.error,
                ),
                onClick = {
                    val client = EngineHolder.client ?: return@TextButton
                    error = null
                    working = true
                    scope.launch {
                        val failure = runCatching { client.setPassword(current, "") }.exceptionOrNull()
                        working = false
                        if (failure == null) onRemoved() else error = securityErrorText(context, failure)
                    }
                },
            ) {
                Text(stringResource(R.string.security_remove_password))
            }
        },
        dismissButton = {
            TextButton(enabled = !working, onClick = onDismiss) {
                Text(stringResource(R.string.action_cancel))
            }
        },
    )
}

@Composable
private fun CreateCodeDialog(
    status: SecurityStatus,
    onDismiss: () -> Unit,
    onCode: (String) -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    var password by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var working by remember { mutableStateOf(false) }

    AlertDialog(
        onDismissRequest = { if (!working) onDismiss() },
        title = { Text(stringResource(R.string.security_code_dialog_title)) },
        text = {
            Column {
                Text(
                    text = stringResource(R.string.security_code_explanation),
                    style = MaterialTheme.typography.bodyMedium,
                )
                if (status.recoveryCodeCreatedAt != 0L) {
                    Spacer(Modifier.height(12.dp))
                    Text(
                        text = stringResource(R.string.security_code_replaces),
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
                error?.let { DialogError(it) }
                // The code opens everything the password does, so the password
                // is asked for again before one is made.
                if (status.passwordSet) {
                    Spacer(Modifier.height(12.dp))
                    PasswordField(
                        value = password,
                        onValueChange = { password = it },
                        label = stringResource(R.string.security_password),
                        imeAction = ImeAction.Done,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        },
        confirmButton = {
            TextButton(
                enabled = !working,
                onClick = {
                    val client = EngineHolder.client ?: return@TextButton
                    error = null
                    working = true
                    scope.launch {
                        val result = runCatching { client.createRecoveryCode(password) }
                        working = false
                        result.fold(
                            onSuccess = onCode,
                            onFailure = { error = securityErrorText(context, it) },
                        )
                    }
                },
            ) {
                Text(stringResource(R.string.security_create_code))
            }
        },
        dismissButton = {
            TextButton(enabled = !working, onClick = onDismiss) {
                Text(stringResource(R.string.action_cancel))
            }
        },
    )
}

/**
 * Shows a recovery code the one time it can be shown, as four rows of two
 * groups. It is closed by its button only, so that a stray tap outside does not
 * lose a code that cannot be shown again.
 */
@Composable
private fun RecoveryCodeDialog(code: String, onDismiss: () -> Unit) {
    val context = LocalContext.current

    AlertDialog(
        onDismissRequest = {},
        properties = DialogProperties(dismissOnBackPress = false, dismissOnClickOutside = false),
        title = { Text(stringResource(R.string.security_code_title)) },
        text = {
            Column(Modifier.fillMaxWidth()) {
                Text(
                    text = stringResource(R.string.security_code_instructions),
                    style = MaterialTheme.typography.bodyMedium,
                )
                Spacer(Modifier.height(16.dp))
                code.split("-").chunked(2).forEach { row ->
                    Text(
                        text = row.joinToString("-"),
                        style = MaterialTheme.typography.titleLarge,
                        fontFamily = FontFamily.Monospace,
                        modifier = Modifier.align(Alignment.CenterHorizontally),
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) {
                Text(stringResource(R.string.security_code_done))
            }
        },
        dismissButton = {
            TextButton(onClick = { copyRecoveryCode(context, code) }) {
                Text(stringResource(R.string.action_copy))
            }
        },
    )
}

private fun copyRecoveryCode(context: Context, code: String) {
    val clipboard = context.getSystemService(ClipboardManager::class.java) ?: return
    val clip = ClipData.newPlainText(context.getString(R.string.security_code_clip_label), code)
    // Keeps the code out of the clipboard preview and off the keyboard's
    // suggestion strip on the versions that have them.
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        clip.description.extras = PersistableBundle().apply {
            putBoolean(ClipDescription.EXTRA_IS_SENSITIVE, true)
        }
    }
    clipboard.setPrimaryClip(clip)
}
