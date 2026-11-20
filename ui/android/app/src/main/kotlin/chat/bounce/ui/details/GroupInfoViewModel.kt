package chat.bounce.ui.details

import android.util.Log
import androidx.compose.runtime.Immutable
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import chat.bounce.data.ChatRepository
import chat.bounce.engine.EngineClient
import chat.bounce.engine.EngineHolder
import chat.bounce.engine.Group
import chat.bounce.engine.Settings
import chat.bounce.engine.User
import chat.bounce.goengine.Goengine
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** Where a per-thread privacy toggle sits relative to the global default. */
enum class GroupInfoPreference { Default, On, Off }

@Immutable
data class GroupMemberRow(
    val id: String,
    val name: String,
    val imageIds: List<String>,
    val isAdmin: Boolean,
    val isSelf: Boolean,
    val online: Boolean,
    val canPromote: Boolean,
    val canDemote: Boolean,
    val canRemove: Boolean,
) {
    /** The overflow button is pointless - and misleading - with nothing behind it. */
    val hasActions: Boolean get() = canPromote || canDemote || canRemove
}

@Immutable
data class GroupInviteRow(
    val id: String,
    val name: String,
    val imageIds: List<String>,
)

@Immutable
data class GroupInfoState(
    val groupId: String = "",
    val loading: Boolean = true,
    /** False once the group has been deleted, blocked, or left. */
    val exists: Boolean = false,
    val name: String = "",
    val imageIds: List<String> = emptyList(),
    val memberCount: Int = 0,
    /** Empty when the creator is unknown, or when it is us - see [createdBySelf]. */
    val createdByName: String = "",
    val createdBySelf: Boolean = false,
    /** Preformatted date, or empty when the group carries no creation time. */
    val createdAtLabel: String = "",
    val isPendingInvite: Boolean = false,
    val invitedByName: String = "",
    val amAdmin: Boolean = false,
    val amMember: Boolean = false,
    /** Name, photo, retention and clear-history are one permission in the engine. */
    val canEdit: Boolean = false,
    val canManageUsers: Boolean = false,
    val retention: Long = 0,
    val muted: Boolean = false,
    val readReceipts: GroupInfoPreference = GroupInfoPreference.Default,
    val typingIndicators: GroupInfoPreference = GroupInfoPreference.Default,
    val defaultReadReceipts: Boolean = true,
    val defaultTypingIndicators: Boolean = true,
    val restrictUserManagement: Boolean = false,
    val restrictGroupEdits: Boolean = false,
    val restrictPosting: Boolean = false,
    val members: List<GroupMemberRow> = emptyList(),
    val invites: List<GroupInviteRow> = emptyList(),
    /** Contacts who are neither members nor already invited. */
    val invitable: List<GroupInviteRow> = emptyList(),
)

/**
 * State and engine plumbing for the group info screen.
 *
 * Every permission flag on [GroupInfoState] mirrors `stateChangeAllowed` in
 * chat/group_state.go. That duplication is deliberate: the engine rejects a
 * forbidden update *after* it has been built and broadcast, so a UI that only
 * knew "admin or not" would offer buttons that silently do nothing. The rules
 * are copied here so an action is either enabled and will apply, or visibly
 * unavailable.
 */
class GroupInfoViewModel(private val groupId: String) : ViewModel() {

    val state: StateFlow<GroupInfoState> = combine(
        ChatRepository.groups,
        ChatRepository.users,
        ChatRepository.profile,
        ChatRepository.settings,
        ChatRepository.onlineUsers,
    ) { groups, users, profile, settings, online ->
        build(groups[groupId], users, profile?.id.orEmpty(), settings, online)
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS),
        initialValue = GroupInfoState(groupId = groupId),
    )

    // --- the group itself ---------------------------------------------------

    fun rename(name: String) = onEngine { it.renameGroup(groupId, name) }

    fun setImage(image: ByteArray) = onEngine { it.setGroupImage(groupId, image) }

    fun setRetention(seconds: Long) = onEngine { it.setGroupRetention(groupId, seconds) }

    fun setMutedUntil(until: Long) = onEngine { it.setGroupMutedUntil(groupId, until) }

    fun setReadReceipts(preference: GroupInfoPreference) = onEngine {
        it.setGroupReadReceiptSettings(groupId, preference.overrides, preference.enabled)
    }

    fun setTypingIndicators(preference: GroupInfoPreference) = onEngine {
        it.setGroupTypingIndicatorSettings(groupId, preference.overrides, preference.enabled)
    }

    // --- members and invites ------------------------------------------------

    fun promote(userId: String) = onEngine { it.promoteGroupAdmin(groupId, userId) }

    fun demote(userId: String) = onEngine { it.demoteGroupAdmin(groupId, userId) }

    fun removeMember(userId: String) = onEngine { it.removeUserFromGroup(groupId, userId) }

    fun invite(userId: String) = onEngine { it.inviteUserToGroup(groupId, userId) }

    fun revokeInvite(userId: String) = onEngine { it.revokeInvite(groupId, userId) }

    fun acceptInvite() = onEngine { it.acceptInvite(groupId) }

    fun declineInvite() = onEngine { it.rejectInvite(groupId) }

    // --- permissions --------------------------------------------------------

    fun setRestrictUserManagement(restricted: Boolean) = onEngine {
        if (restricted) it.restrictUserManagement(groupId) else it.unrestrictUserManagement(groupId)
    }

    fun setRestrictGroupEdits(restricted: Boolean) = onEngine {
        if (restricted) it.restrictGroupEdits(groupId) else it.unrestrictGroupEdits(groupId)
    }

    fun setRestrictPosting(restricted: Boolean) = onEngine {
        if (restricted) it.restrictPosting(groupId) else it.unrestrictPosting(groupId)
    }

    // --- destructive --------------------------------------------------------

    fun clearHistory() = onEngine { it.clearGroupChatHistory(groupId) }

    /** Permanent: the engine has no unblock, and the group can never re-invite us. */
    fun blockGroup() = onEngine { it.blockGroup(groupId) }

    /**
     * The engine models leaving as removing yourself, which is the one removal
     * it allows regardless of who manages users. [deleteGroup] is a different
     * act - it ends the group for everybody - and is admin-only.
     */
    fun leaveGroup() {
        val me = ChatRepository.currentUserId
        if (me.isEmpty()) {
            Log.w(TAG, "cannot leave $groupId: no profile loaded")
            return
        }
        onEngine { it.removeUserFromGroup(groupId, me) }
    }

    fun deleteGroup() = onEngine { it.deleteGroup(groupId) }

    // --- internals ----------------------------------------------------------

    private fun build(
        group: Group?,
        users: Map<String, User>,
        me: String,
        settings: Settings,
        online: Set<String>,
    ): GroupInfoState {
        if (group == null) return GroupInfoState(groupId = groupId, loading = false, exists = false)

        val admins = group.admins.toSet()
        val amAdmin = me in admins
        val amMember = group.users.any { it.id == me }
        val pending = group.isPendingInvite

        // An invitee is not yet a member, and the engine refuses every update
        // from a non-member except responding to the invite and blocking.
        val active = amMember && !pending
        val canEdit = active && (!group.restrictGroupEdits || amAdmin)
        val canManageUsers = active && (!group.restrictUserManagement || amAdmin)
        // A group whose admins have all left has none at all; the engine lets any
        // member appoint the first one rather than leaving it permanently stuck.
        val canPromote = active && (amAdmin || admins.isEmpty())

        val members = group.users
            .map { member ->
                // The repository's copy carries the alias and the newest photo;
                // the group's own copy is only a fallback for someone we learned
                // about through this group and nowhere else.
                val known = users[member.id] ?: member
                val isSelf = member.id == me
                val isAdmin = member.id in admins
                GroupMemberRow(
                    id = member.id,
                    name = known.displayName,
                    imageIds = known.images,
                    isAdmin = isAdmin,
                    isSelf = isSelf,
                    online = !isSelf && member.id in online,
                    canPromote = canPromote && !isAdmin,
                    canDemote = active && amAdmin && isAdmin,
                    // Removing yourself is always permitted, but that is "leave
                    // group" and belongs in the danger zone, not in a row menu.
                    canRemove = canManageUsers && !isSelf,
                )
            }
            .sortedWith(
                compareByDescending<GroupMemberRow> { it.isAdmin }
                    .thenBy { it.name.lowercase(Locale.getDefault()) }
            )

        val invites = group.invites
            .map { invitee ->
                val known = users[invitee.id] ?: invitee
                GroupInviteRow(invitee.id, known.displayName, known.images)
            }
            .sortedBy { it.name.lowercase(Locale.getDefault()) }

        val taken = HashSet<String>(group.users.size + group.invites.size)
        group.users.mapTo(taken) { it.id }
        group.invites.mapTo(taken) { it.id }
        // Only contacts can be invited: an invitation carries an onion address,
        // and the engine will not hand one out for somebody we never paired with.
        val invitable = users.values
            .filter { it.id != me && !it.blocked && it.id !in taken }
            .map { GroupInviteRow(it.id, it.displayName, it.images) }
            .sortedBy { it.name.lowercase(Locale.getDefault()) }

        val createdBySelf = group.createdBy.isNotEmpty() && group.createdBy == me

        return GroupInfoState(
            groupId = groupId,
            loading = false,
            exists = true,
            name = group.name,
            imageIds = group.images,
            memberCount = group.users.size,
            createdByName = if (createdBySelf) "" else users[group.createdBy]?.displayName.orEmpty(),
            createdBySelf = createdBySelf,
            createdAtLabel = formatDate(group.createdAt),
            isPendingInvite = pending,
            invitedByName = users[group.invitedBy]?.displayName.orEmpty(),
            amAdmin = amAdmin,
            amMember = amMember,
            canEdit = canEdit,
            canManageUsers = canManageUsers,
            retention = group.retention,
            muted = group.mutedUntil == Goengine.MutedForever ||
                group.mutedUntil > System.currentTimeMillis() / 1000,
            readReceipts = preferenceOf(
                group.overrideReadReceiptSetting,
                group.readReceiptsEnabled,
            ),
            typingIndicators = preferenceOf(
                group.overrideTypingIndicatorSetting,
                group.typingIndicatorsEnabled,
            ),
            defaultReadReceipts = settings.defaultSendReadReceipts,
            defaultTypingIndicators = settings.defaultSendTypingIndicators,
            restrictUserManagement = group.restrictUserManagement,
            restrictGroupEdits = group.restrictGroupEdits,
            restrictPosting = group.restrictPosting,
            members = members,
            invites = invites,
            invitable = invitable,
        )
    }

    private fun formatDate(unixSeconds: Long): String =
        if (unixSeconds <= 0) ""
        else createdFormat.format(Instant.ofEpochSecond(unixSeconds).atZone(ZoneId.systemDefault()))

    /**
     * Engine calls are blocking JNI and any of them can fail - offline, revoked
     * device, or a permission that changed between render and tap. A failure
     * must not take the process down and there is nothing to retry: the engine
     * re-emits the authoritative group state either way, so the UI corrects
     * itself.
     */
    private fun onEngine(block: suspend (EngineClient) -> Unit) {
        val client = EngineHolder.client
        if (client == null) {
            Log.w(TAG, "engine not started, dropping group action")
            return
        }
        viewModelScope.launch {
            runCatching { block(client) }.onFailure { Log.w(TAG, "engine call failed", it) }
        }
    }

    companion object {
        private const val TAG = "GroupInfo"
        private const val STOP_TIMEOUT_MS = 5_000L

        private val createdFormat =
            DateTimeFormatter.ofPattern("MMM d, yyyy", Locale.getDefault())

        fun factory(groupId: String): ViewModelProvider.Factory = viewModelFactory {
            initializer { GroupInfoViewModel(groupId) }
        }
    }
}

private fun preferenceOf(override: Boolean, enabled: Boolean): GroupInfoPreference = when {
    !override -> GroupInfoPreference.Default
    enabled -> GroupInfoPreference.On
    else -> GroupInfoPreference.Off
}

private val GroupInfoPreference.overrides: Boolean
    get() = this != GroupInfoPreference.Default

/**
 * The engine ignores the enabled flag while override is false, and the desktop
 * client sends `true` there. Matching it keeps the stored pair identical across
 * our own devices, so a later read of the raw column never disagrees.
 */
private val GroupInfoPreference.enabled: Boolean
    get() = this != GroupInfoPreference.Off
