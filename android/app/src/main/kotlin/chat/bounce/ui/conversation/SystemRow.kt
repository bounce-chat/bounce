package chat.bounce.ui.conversation

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.data.ConversationItem
import chat.bounce.data.SystemEventKind

/**
 * A centred, muted status line - group membership changes, retention changes,
 * profile renames. These never count as unread and never get a bubble.
 *
 * [ConversationItem.SystemEvent] carries the *data* behind the sentence, not the
 * sentence itself, so this is where user ids become display names and where the
 * localized strings are picked.
 */
@Composable
fun SystemRow(
    event: ConversationItem.SystemEvent,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = 32.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.Center,
    ) {
        Text(
            text = systemEventText(event),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
    }
}

/**
 * The human sentence for a system event, with "You" substituted for the local
 * user so the history reads the same way the desktop client's does.
 *
 * The `when` is exhaustive with no `else` on purpose: a kind added to the engine
 * should fail this build rather than render a blank row in it.
 */
@Composable
private fun systemEventText(event: ConversationItem.SystemEvent): String {
    val actor = actorName(event.actorId)
    val target = targetName(event.targetUserId, event.name)
    val isSelfActor = event.actorId.isNotBlank() && event.actorId == ChatRepository.currentUserId

    return when (event.kind) {
        SystemEventKind.RETENTION_CHANGED ->
            if (event.retention <= 0L) {
                stringResource(R.string.conv_sys_retention_off, actor)
            } else {
                stringResource(
                    R.string.conv_sys_retention_set,
                    actor,
                    formatRetention(event.retention),
                )
            }

        SystemEventKind.HISTORY_CLEARED ->
            stringResource(R.string.conv_sys_history_cleared, actor)

        SystemEventKind.GROUP_RENAMED ->
            stringResource(R.string.conv_sys_group_renamed, actor, event.name)

        SystemEventKind.USER_INVITED ->
            stringResource(R.string.conv_sys_user_invited, actor, target)

        // The engine emits one frame for both leaving and being removed, and
        // distinguishes them by the actor being their own target.
        SystemEventKind.USER_REMOVED ->
            if (event.targetUserId == null || event.targetUserId == event.actorId) {
                stringResource(R.string.conv_sys_user_left, actor)
            } else {
                stringResource(R.string.conv_sys_user_removed, actor, target)
            }

        SystemEventKind.INVITE_REVOKED ->
            stringResource(R.string.conv_sys_invite_revoked, actor, target)

        SystemEventKind.INVITE_ACCEPTED ->
            stringResource(R.string.conv_sys_invite_accepted, actor)

        SystemEventKind.INVITE_REJECTED ->
            stringResource(R.string.conv_sys_invite_rejected, actor)

        SystemEventKind.ADMIN_PROMOTED ->
            stringResource(R.string.conv_sys_admin_promoted, actor, target)

        SystemEventKind.ADMIN_DEMOTED ->
            stringResource(R.string.conv_sys_admin_demoted, actor, target)

        SystemEventKind.USER_MANAGEMENT_RESTRICTED ->
            stringResource(R.string.conv_sys_user_management_restricted, actor)

        SystemEventKind.USER_MANAGEMENT_UNRESTRICTED ->
            stringResource(R.string.conv_sys_user_management_unrestricted, actor)

        SystemEventKind.GROUP_EDITS_RESTRICTED ->
            stringResource(R.string.conv_sys_group_edits_restricted, actor)

        SystemEventKind.GROUP_EDITS_UNRESTRICTED ->
            stringResource(R.string.conv_sys_group_edits_unrestricted, actor)

        SystemEventKind.POSTING_RESTRICTED ->
            stringResource(R.string.conv_sys_posting_restricted, actor)

        SystemEventKind.POSTING_UNRESTRICTED ->
            stringResource(R.string.conv_sys_posting_unrestricted, actor)

        SystemEventKind.USER_BLOCKED_GROUP ->
            stringResource(R.string.conv_sys_user_blocked_group, actor)

        SystemEventKind.GROUP_IMAGE_CHANGED ->
            stringResource(R.string.conv_sys_group_image_changed, actor)

        // A rename reads better against the name people already knew, so the old
        // name is the subject of the sentence rather than the current one.
        SystemEventKind.USER_RENAMED ->
            if (isSelfActor) {
                stringResource(R.string.conv_sys_user_renamed_self, event.name)
            } else {
                stringResource(
                    R.string.conv_sys_user_renamed,
                    event.previousName.takeIf { it.isNotBlank() } ?: actor,
                    event.name,
                )
            }

        SystemEventKind.USER_IMAGE_CHANGED ->
            if (isSelfActor) {
                stringResource(R.string.conv_sys_user_image_changed_self)
            } else {
                stringResource(R.string.conv_sys_user_image_changed, actor)
            }

        // An alias is local, so the person is named by the name they gave rather
        // than by the alias that is being set on top of it.
        SystemEventKind.ALIAS_SET -> {
            val aliased = ChatRepository.userById(event.targetUserId ?: event.actorId)
                ?.name?.takeIf { it.isNotBlank() } ?: target
            if (event.name.isBlank()) {
                stringResource(R.string.conv_sys_alias_cleared, aliased)
            } else {
                stringResource(R.string.conv_sys_alias_set, aliased, event.name)
            }
        }
    }
}

/** The name of whoever performed the action, in subject position. */
@Composable
private fun actorName(userId: String): String {
    if (userId.isNotBlank() && userId == ChatRepository.currentUserId) {
        return stringResource(R.string.conv_sys_you)
    }
    val known = ChatRepository.userById(userId)?.displayName?.takeIf { it.isNotBlank() }
    return known ?: stringResource(R.string.conv_sys_someone)
}

/**
 * The name of whoever the action was performed on, in object position. An
 * invited user is not in the directory until the invite is accepted, so the
 * name the event carries is the next best source before a generic noun.
 */
@Composable
private fun targetName(userId: String?, eventName: String): String {
    if (userId != null && userId.isNotBlank() && userId == ChatRepository.currentUserId) {
        return stringResource(R.string.conv_sys_you_object)
    }
    val known = userId?.let { ChatRepository.userById(it) }
        ?.displayName?.takeIf { it.isNotBlank() }
    val named = known ?: eventName.takeIf { it.isNotBlank() }
    return named ?: stringResource(R.string.conv_sys_someone)
}

/** The duration fragment of the retention sentence: "1 week", "3 days", "45 minutes". */
@Composable
private fun formatRetention(seconds: Long): String = when {
    seconds <= 0L -> stringResource(R.string.conv_sys_duration_off)
    seconds % WEEK == 0L -> counted(
        seconds / WEEK,
        R.string.conv_sys_duration_1_week,
        R.string.conv_sys_duration_weeks,
    )
    seconds % DAY == 0L -> counted(
        seconds / DAY,
        R.string.conv_sys_duration_1_day,
        R.string.conv_sys_duration_days,
    )
    seconds % HOUR == 0L -> counted(
        seconds / HOUR,
        R.string.conv_sys_duration_1_hour,
        R.string.conv_sys_duration_hours,
    )
    // Sub-minute retentions exist on the wire but read as noise, so they round up.
    else -> counted(
        (seconds / MINUTE).coerceAtLeast(1L),
        R.string.conv_sys_duration_1_minute,
        R.string.conv_sys_duration_minutes,
    )
}

@Composable
private fun counted(count: Long, one: Int, many: Int): String =
    if (count == 1L) stringResource(one) else stringResource(many, count)

private const val MINUTE = 60L
private const val HOUR = 3_600L
private const val DAY = 86_400L
private const val WEEK = 604_800L
