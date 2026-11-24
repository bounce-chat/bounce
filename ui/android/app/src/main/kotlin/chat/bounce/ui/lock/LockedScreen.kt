package chat.bounce.ui.lock

import android.content.Context
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import chat.bounce.BounceApplication
import chat.bounce.R
import chat.bounce.engine.EngineHolder
import chat.bounce.goengine.Goengine
import chat.bounce.ui.components.PasswordField
import kotlinx.coroutines.launch

/**
 * What stands between a start and the app when the install does not open with
 * nothing asked: the password, the recovery code, and the screens for data that
 * cannot be opened at all.
 *
 * [reason] is [EngineHolder.lockReason], which is the only thing that puts this
 * on screen. Every attempt to open the install clears it, so the screen goes
 * away while the attempt runs and comes back, on whichever step the new reason
 * calls for, if the attempt failed. That is also why no step here keeps
 * anything across an attempt.
 */
@Composable
fun LockedScreen(reason: String, modifier: Modifier = Modifier) {
    val context = LocalContext.current.applicationContext

    // Where the user has gone from the step the reason opens on.
    var step by remember(reason) { mutableStateOf(firstStep(reason)) }
    var deadEnd by remember(reason) {
        mutableStateOf(
            if (reason == Goengine.ErrorKeyFileDamaged) DeadEnd.KeyFileDamaged else DeadEnd.KeyFileMissing,
        )
    }
    // What a recovery set the password to, for the open that follows it.
    var recoveredPassword by remember(reason) { mutableStateOf("") }

    Surface(modifier = modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        when (step) {
            Step.Unlock -> UnlockStep(
                wrongPassword = reason == Goengine.ErrorWrongPassword,
                onUnlock = { open(context, it) },
                onForgot = { step = Step.Recovery },
            )

            Step.Recovery -> RecoveryStep(
                explanation = stringResource(
                    if (reason == Goengine.ErrorKeystoreSecretMissing) {
                        R.string.lock_recover_keystore
                    } else {
                        R.string.lock_recover_forgotten
                    },
                ),
                onRecovered = {
                    recoveredPassword = it
                    step = Step.Recovered
                },
                onDeadEnd = {
                    deadEnd = it
                    step = Step.DeadEnd
                },
                // Recovery is the only way on when it is where the reason starts.
                onBack = if (firstStep(reason) == Step.Unlock) ({ step = Step.Unlock }) else null,
            )

            Step.Recovered -> RecoveredStep(
                hasPassword = recoveredPassword.isNotEmpty(),
                onOpen = { open(context, recoveredPassword) },
            )

            Step.DeadEnd -> DeadEndStep(
                cause = deadEnd,
                onReset = { open(context, "") },
                onBack = if (firstStep(reason) != Step.DeadEnd) ({ step = Step.Recovery }) else null,
            )

            Step.Failed -> FailedStep(
                reason = reason,
                onRetry = { open(context, "") },
            )
        }
    }
}

private enum class Step { Unlock, Recovery, Recovered, DeadEnd, Failed }

/** Why the data cannot be opened by anything the user has. */
private enum class DeadEnd(val text: Int) {
    NoCode(R.string.lock_dead_end_no_code),
    NeverHadCode(R.string.lock_dead_end_never_had_code),
    KeyFileMissing(R.string.lock_dead_end_key_file_missing),
    KeyFileDamaged(R.string.lock_dead_end_key_file_damaged),
}

private fun firstStep(reason: String): Step = when (reason) {
    Goengine.ErrorPasswordRequired, Goengine.ErrorWrongPassword -> Step.Unlock
    Goengine.ErrorKeystoreSecretMissing -> Step.Recovery
    Goengine.ErrorKeyFileMissing, Goengine.ErrorKeyFileDamaged -> Step.DeadEnd
    else -> Step.Failed
}

/**
 * On the application's scope and not the screen's: the attempt begins by
 * clearing the reason this screen is shown for, which takes the screen and any
 * scope of its own away before the attempt has got anywhere.
 */
private fun open(context: Context, password: String) {
    BounceApplication.scope.launch { EngineHolder.start(context, password) }
}

@Composable
private fun UnlockStep(
    wrongPassword: Boolean,
    onUnlock: (String) -> Unit,
    onForgot: () -> Unit,
) {
    var password by remember { mutableStateOf("") }

    LockColumn(stringResource(R.string.lock_unlock_title)) {
        if (wrongPassword) {
            LockError(stringResource(R.string.lock_wrong_password))
        }
        PasswordField(
            value = password,
            onValueChange = { password = it },
            label = stringResource(R.string.lock_password),
            imeAction = ImeAction.Done,
            onDone = { if (password.isNotEmpty()) onUnlock(password) },
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(16.dp))
        Button(
            enabled = password.isNotEmpty(),
            onClick = { onUnlock(password) },
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(stringResource(R.string.lock_unlock))
        }
        Spacer(Modifier.height(8.dp))
        TextButton(onClick = onForgot) {
            Text(stringResource(R.string.lock_forgot_password))
        }
    }
}

/**
 * @param onBack returns to the step the user came from, null when recovery is
 *   the only way on.
 */
@Composable
private fun RecoveryStep(
    explanation: String,
    onRecovered: (String) -> Unit,
    onDeadEnd: (DeadEnd) -> Unit,
    onBack: (() -> Unit)?,
) {
    val context = LocalContext.current.applicationContext
    val scope = rememberCoroutineScope()

    var code by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var working by remember { mutableStateOf(false) }

    val passwordsDiffer = stringResource(R.string.lock_passwords_differ)
    val codeMistyped = stringResource(R.string.lock_code_mistyped)
    val codeWrong = stringResource(R.string.lock_code_wrong)

    BackHandler(enabled = onBack != null) { onBack?.invoke() }

    LockColumn(stringResource(R.string.lock_recover_title)) {
        LockText(explanation)
        error?.let { LockError(it) }
        OutlinedTextField(
            value = code,
            onValueChange = { code = it },
            label = { Text(stringResource(R.string.lock_recovery_code)) },
            textStyle = LocalTextStyle.current.copy(fontFamily = FontFamily.Monospace),
            keyboardOptions = KeyboardOptions(
                capitalization = KeyboardCapitalization.Characters,
                imeAction = ImeAction.Next,
            ),
            maxLines = 2,
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(8.dp))
        PasswordField(
            value = password,
            onValueChange = { password = it },
            label = stringResource(R.string.lock_new_password),
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(8.dp))
        PasswordField(
            value = confirm,
            onValueChange = { confirm = it },
            label = stringResource(R.string.lock_confirm_password),
            imeAction = ImeAction.Done,
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(16.dp))
        Button(
            enabled = !working && code.isNotBlank(),
            onClick = {
                if (password != confirm) {
                    error = passwordsDiffer
                    return@Button
                }
                val newPassword = password
                error = null
                working = true
                scope.launch {
                    val failure = runCatching { EngineHolder.recover(context, code, newPassword) }.exceptionOrNull()
                    working = false
                    if (failure == null) {
                        onRecovered(newPassword)
                        return@launch
                    }
                    when (failure.message) {
                        Goengine.ErrorMistypedRecoveryCode -> error = codeMistyped
                        Goengine.ErrorWrongRecoveryCode -> error = codeWrong
                        Goengine.ErrorNoRecoveryCode -> onDeadEnd(DeadEnd.NeverHadCode)
                        Goengine.ErrorKeyFileMissing -> onDeadEnd(DeadEnd.KeyFileMissing)
                        Goengine.ErrorKeyFileDamaged -> onDeadEnd(DeadEnd.KeyFileDamaged)
                        else -> error = context.getString(R.string.lock_recover_failed, failure.message.orEmpty())
                    }
                }
            },
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(stringResource(R.string.lock_recover))
        }
        Spacer(Modifier.height(8.dp))
        TextButton(onClick = { onDeadEnd(DeadEnd.NoCode) }) {
            Text(stringResource(R.string.lock_no_code))
        }
        if (onBack != null) {
            TextButton(onClick = onBack) {
                Text(stringResource(R.string.action_back))
            }
        }
    }
}

@Composable
private fun RecoveredStep(hasPassword: Boolean, onOpen: () -> Unit) {
    LockColumn(stringResource(R.string.lock_recovered_title)) {
        LockText(
            stringResource(
                if (hasPassword) R.string.lock_recovered_password else R.string.lock_recovered_no_password,
            ),
        )
        LockText(stringResource(R.string.lock_recovered_settings))
        Button(onClick = onOpen, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(R.string.lock_open))
        }
    }
}

/**
 * For an install whose data nothing the user has can open. What is left is to
 * start over on this device.
 *
 * @param onReset runs once the install has been deleted.
 * @param onBack returns to the step the user came from, null when there is
 *   none.
 */
@Composable
private fun DeadEndStep(
    cause: DeadEnd,
    onReset: () -> Unit,
    onBack: (() -> Unit)?,
) {
    val context = LocalContext.current.applicationContext
    val scope = rememberCoroutineScope()

    var confirming by remember { mutableStateOf(false) }
    var working by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    BackHandler(enabled = onBack != null) { onBack?.invoke() }

    if (confirming) {
        AlertDialog(
            onDismissRequest = { confirming = false },
            title = { Text(stringResource(R.string.lock_reset_confirm_title)) },
            text = { Text(stringResource(R.string.lock_reset_confirm_body)) },
            confirmButton = {
                TextButton(
                    colors = ButtonDefaults.textButtonColors(
                        contentColor = MaterialTheme.colorScheme.error,
                    ),
                    onClick = {
                        confirming = false
                        error = null
                        working = true
                        scope.launch {
                            val failure = runCatching { EngineHolder.reset(context) }.exceptionOrNull()
                            working = false
                            if (failure == null) {
                                onReset()
                            } else {
                                error = context.getString(R.string.lock_reset_failed, failure.message.orEmpty())
                            }
                        }
                    },
                ) {
                    Text(stringResource(R.string.lock_reset_confirm))
                }
            },
            dismissButton = {
                TextButton(onClick = { confirming = false }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
    }

    LockColumn(stringResource(R.string.lock_dead_end_title)) {
        LockText(stringResource(cause.text))
        LockText(stringResource(R.string.lock_reset_explanation))
        error?.let { LockError(it) }
        Button(
            enabled = !working,
            colors = ButtonDefaults.buttonColors(
                containerColor = MaterialTheme.colorScheme.error,
                contentColor = MaterialTheme.colorScheme.onError,
            ),
            onClick = { confirming = true },
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(stringResource(R.string.lock_reset))
        }
        if (onBack != null) {
            Spacer(Modifier.height(8.dp))
            TextButton(onClick = onBack) {
                Text(stringResource(R.string.action_back))
            }
        }
    }
}

/** A failure that trying again may clear, such as a keystore that did not answer. */
@Composable
private fun FailedStep(reason: String, onRetry: () -> Unit) {
    val keystore = reason == Goengine.ErrorKeystoreUnavailable

    LockColumn(
        stringResource(
            if (keystore) R.string.lock_keystore_unavailable_title else R.string.lock_failed_title,
        ),
    ) {
        LockText(if (keystore) stringResource(R.string.lock_keystore_unavailable) else reason)
        Button(onClick = onRetry, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(R.string.lock_try_again))
        }
    }
}

/** The logo and a title above one step's content, scrolling so the keyboard never covers a field. */
@Composable
private fun LockColumn(title: String, content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .safeDrawingPadding()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Spacer(Modifier.height(48.dp))
        Image(
            painter = painterResource(R.drawable.ic_bounce_logo),
            contentDescription = stringResource(R.string.app_name),
            modifier = Modifier.size(96.dp),
        )
        Spacer(Modifier.height(16.dp))
        Text(
            text = title,
            style = MaterialTheme.typography.headlineSmall,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(16.dp))
        content()
        Spacer(Modifier.height(24.dp))
    }
}

@Composable
private fun LockText(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
        modifier = Modifier.padding(bottom = 16.dp),
    )
}

@Composable
private fun LockError(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.error,
        textAlign = TextAlign.Center,
        modifier = Modifier.padding(bottom = 16.dp),
    )
}
