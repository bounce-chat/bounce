package chat.bounce.ui.share

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.Forum
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import chat.bounce.R
import chat.bounce.data.ChatRepository
import chat.bounce.data.PendingShare
import chat.bounce.ui.components.Avatar
import chat.bounce.ui.components.EmptyState

/**
 * Where a share lands when the user picked Bounce rather than one of its
 * conversations.
 *
 * Deliberately not the inbox in a different mode: this list has no unread
 * badges, no drafts, no swipe actions and no long-press menu, because every one
 * of those would be a way to do something other than the single thing the user
 * is here to do.
 *
 * Backing out abandons the share rather than leaving it pending - otherwise the
 * next conversation opened would silently inherit somebody's photos.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ShareTargetScreen(
    onPick: (String) -> Unit,
    onCancel: () -> Unit,
) {
    val threads by ChatRepository.threads.collectAsStateWithLifecycle()
    val payload by PendingShare.pending.collectAsStateWithLifecycle()

    val cancel = {
        PendingShare.clear()
        onCancel()
    }
    BackHandler(onBack = cancel)

    val attachmentCount = payload?.uris?.size ?: 0

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(stringResource(R.string.share_title))
                        if (attachmentCount > 0) {
                            Text(
                                text = pluralStringResource(
                                    R.plurals.share_subtitle_items,
                                    attachmentCount,
                                    attachmentCount,
                                ),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                },
                navigationIcon = {
                    IconButton(onClick = cancel) {
                        Icon(
                            Icons.Filled.Close,
                            contentDescription = stringResource(R.string.action_cancel),
                        )
                    }
                },
            )
        },
    ) { padding ->
        if (threads.isEmpty()) {
            EmptyState(
                icon = Icons.Outlined.Forum,
                title = stringResource(R.string.share_no_threads_title),
                description = stringResource(R.string.share_no_threads_body),
                modifier = Modifier.padding(padding),
            )
            return@Scaffold
        }

        LazyColumn(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize(),
        ) {
            items(threads, key = { it.id }) { thread ->
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable { onPick(thread.id) }
                        .padding(horizontal = 16.dp, vertical = 10.dp),
                ) {
                    Avatar(
                        fileIds = listOfNotNull(thread.avatarFileId),
                        fallbackId = thread.id,
                        fallbackName = thread.name,
                        size = 44.dp,
                    )
                    Spacer(Modifier.width(12.dp))
                    Text(
                        text = thread.name,
                        style = MaterialTheme.typography.bodyLarge,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
    }
}
