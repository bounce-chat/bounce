package chat.bounce.ui.components

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import chat.bounce.R

// The disappearing-message timer control, shared by the two "new conversation"
// defaults in settings and by new-group creation.
//
// Retentions are plain second counts on the wire, and a peer running the desktop
// client can set any value it likes, so the presets below are only a shortcut -
// retentionLabel has to be able to name an arbitrary number too.

/** Preset retention values, in seconds. Zero means "keep messages forever". */
object Retention {
    const val OFF: Long = 0
    const val ONE_DAY: Long = 24 * 60 * 60
    const val ONE_WEEK: Long = 7 * 24 * 60 * 60

    /**
     * Four weeks rather than a calendar month, matching the desktop client's
     * "1 Month" value exactly so the two never disagree about what a thread's
     * timer means.
     */
    const val FOUR_WEEKS: Long = 4 * 7 * 24 * 60 * 60

    val presets: List<Long> = listOf(OFF, ONE_DAY, ONE_WEEK, FOUR_WEEKS)
}

@Composable
fun retentionLabel(seconds: Long): String = when (seconds) {
    Retention.OFF -> stringResource(R.string.retention_off)
    Retention.ONE_DAY -> stringResource(R.string.retention_one_day)
    Retention.ONE_WEEK -> stringResource(R.string.retention_one_week)
    Retention.FOUR_WEEKS -> stringResource(R.string.retention_four_weeks)
    else -> when {
        seconds < 60 -> stringResource(R.string.retention_seconds, seconds)
        seconds < 60 * 60 -> stringResource(R.string.retention_minutes, seconds / 60)
        seconds < 24 * 60 * 60 -> stringResource(R.string.retention_hours, seconds / (60 * 60))
        else -> stringResource(R.string.retention_days, seconds / (24 * 60 * 60))
    }
}

/**
 * A dropdown of [Retention.presets]. A non-preset [value] - one a peer chose -
 * is shown as an extra entry so selecting it back is possible after browsing
 * the list.
 */
@Composable
fun RetentionPicker(
    value: Long,
    onValueChange: (Long) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    var expanded by remember { mutableStateOf(false) }
    val options = remember(value) {
        if (value in Retention.presets) Retention.presets else Retention.presets + value
    }

    Box(modifier) {
        OutlinedButton(
            onClick = { expanded = true },
            enabled = enabled,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(retentionLabel(value), modifier = Modifier.weight(1f))
            Icon(Icons.Filled.ArrowDropDown, contentDescription = null)
        }
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            options.forEach { option ->
                DropdownMenuItem(
                    text = { Text(retentionLabel(option)) },
                    onClick = {
                        expanded = false
                        if (option != value) onValueChange(option)
                    },
                )
            }
        }
    }
}

/** Label + picker, laid out for the settings and new-group forms. */
@Composable
fun LabelledRetentionPicker(
    label: String,
    value: Long,
    onValueChange: (Long) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    Column(modifier.fillMaxWidth()) {
        Text(text = label, style = MaterialTheme.typography.bodyLarge)
        Spacer(Modifier.height(6.dp))
        RetentionPicker(
            value = value,
            onValueChange = onValueChange,
            enabled = enabled,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}
