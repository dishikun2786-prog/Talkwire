package io.talkwire.ui.screens

import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.PersonAdd
import androidx.compose.material3.Divider
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.talkwire.core.client.disbandGroup
import io.talkwire.core.client.kick
import io.talkwire.core.client.leaveGroup
import io.talkwire.core.client.renameGroup
import io.talkwire.core.client.setMemberRole
import io.talkwire.core.client.transferOwner
import io.talkwire.core.engine.ChatEvent
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.GroupMember
import io.talkwire.core.model.GroupRole
import io.talkwire.core.protocol.Reply
import io.talkwire.ui.R
import io.talkwire.ui.components.ImAlertDialog
import io.talkwire.ui.components.ImAvatar
import io.talkwire.ui.components.ImConfirmDialog
import io.talkwire.ui.host.ImDeepLink
import io.talkwire.ui.host.ProfileCache
import io.talkwire.ui.theme.ImTheme
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.launch

/**
 * What [me] may do to [target] in a group. Mirrors the server's checks, so
 * the UI does not offer what the server will refuse; the server stays the
 * authority.
 */
internal data class MemberPermissions(val setRole: Boolean, val kick: Boolean, val transfer: Boolean) {
    val any: Boolean get() = setRole || kick || transfer

    companion object {
        fun of(me: GroupRole, myUid: String, target: GroupMember): MemberPermissions {
            if (target.uid == myUid || target.role == GroupRole.OWNER) return MemberPermissions(false, false, false)
            return MemberPermissions(
                setRole = me == GroupRole.OWNER,
                // Admins remove members but not other admins.
                kick = me == GroupRole.OWNER || (me == GroupRole.ADMIN && target.role == GroupRole.MEMBER),
                transfer = me == GroupRole.OWNER,
            )
        }
    }
}

/** Owners first, then admins, then members by when they joined. */
internal val MemberOrder: Comparator<GroupMember> = compareBy<GroupMember> { it.role.ordinal }.thenBy { it.joinedAt }

private sealed interface Pending {
    data class Kick(val uid: String) : Pending
    data class Transfer(val uid: String) : Pending
    data object Leave : Pending
    data object Disband : Pending
    data object Clear : Pending
    data object Rename : Pending
}

@Composable
internal fun InfoScreen(state: ImChatState, id: ConversationId, onBack: () -> Unit) {
    val engine = state.engine
    val context = LocalContext.current
    val colors = ImTheme.colors
    val scope = rememberCoroutineScope()
    val summaries by engine.conversations.collectAsState()
    val summary = summaries.firstOrNull { it.id == id }
    val groups by engine.groups.collectAsState()
    val people by state.profiles.profiles.collectAsState()
    var members by remember(id) { mutableStateOf<List<GroupMember>>(emptyList()) }
    var pending by remember { mutableStateOf<Pending?>(null) }
    var menuFor by remember { mutableStateOf<String?>(null) }
    var newName by remember { mutableStateOf("") }

    val myRole = members.firstOrNull { it.uid == engine.me }?.role ?: GroupRole.MEMBER
    val title = state.title(id, summary, groups, people)
    fun name(uid: String) = people[uid]?.name?.takeIf { it.isNotBlank() } ?: ProfileCache.shortUid(uid)
    fun toast(res: Int) = Toast.makeText(context, context.getString(res), Toast.LENGTH_SHORT).show()

    suspend fun reload() {
        engine.members(id.id, refresh = true)?.let { list ->
            members = list.sortedWith(MemberOrder)
            state.profiles.request(list.map { it.uid })
        }
    }
    if (id.isGroup) {
        LaunchedEffect(id) {
            reload()
            engine.events.filter { it is ChatEvent.GroupChanged && it.conversation == id }.collect { reload() }
        }
    } else {
        LaunchedEffect(id) { state.profiles.request(listOf(id.id)) }
    }

    /** Runs a group request; a refused or failed one shows a message and keeps the screen as it is. */
    fun act(onDone: suspend () -> Unit = { reload() }, call: suspend () -> Reply) {
        scope.launch {
            val reply = try {
                call()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                null
            }
            when {
                reply == null -> toast(R.string.talkwire_action_failed)
                reply.ok -> onDone()
                reply.code == 409 -> toast(R.string.talkwire_admin_limit)
                else -> toast(R.string.talkwire_action_failed)
            }
        }
    }
    val leaveChat: suspend () -> Unit = {
        runCatching { engine.refreshGroups() }
        state.open(ImDeepLink.Conversations)
    }

    Column(Modifier.fillMaxSize().background(colors.background)) {
        SimpleTopBar(stringResource(if (id.isGroup) R.string.talkwire_group_info else R.string.talkwire_chat_info), onBack)
        LazyColumn(Modifier.fillMaxSize().testTag("talkwire_info")) {
            item {
                Row(Modifier.fillMaxWidth().background(colors.surface).padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
                    ImAvatar(if (id.isGroup) null else state.host.resolveMediaUrl(people[id.id]?.avatarUrl), title, size = 56.dp)
                    Spacer(Modifier.width(12.dp))
                    Text(title, fontSize = 18.sp, fontWeight = FontWeight.Medium, color = colors.textPrimary, modifier = Modifier.weight(1f))
                }
                Spacer(Modifier.height(8.dp))
            }
            if (id.isGroup) {
                item {
                    Text(
                        stringResource(R.string.talkwire_members_count, members.size),
                        fontSize = 13.sp,
                        color = colors.textSecondary,
                        modifier = Modifier.padding(start = 16.dp, top = 8.dp, bottom = 4.dp),
                    )
                }
                items(members, key = { it.uid }) { m ->
                    val perms = MemberPermissions.of(myRole, engine.me, m)
                    Box {
                        Row(
                            Modifier.fillMaxWidth().background(colors.surface)
                                // Rows open nothing for plain members: members cannot view each other's profiles.
                                .clickable(enabled = perms.any) { menuFor = m.uid }
                                .padding(horizontal = 16.dp, vertical = 10.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            ImAvatar(state.host.resolveMediaUrl(people[m.uid]?.avatarUrl), name(m.uid), size = 40.dp)
                            Spacer(Modifier.width(12.dp))
                            Text(if (m.uid == engine.me) context.getString(R.string.talkwire_you) else name(m.uid), fontSize = 15.sp, color = colors.textPrimary, modifier = Modifier.weight(1f))
                            if (m.role != GroupRole.MEMBER) {
                                Surface(shape = RoundedCornerShape(4.dp), color = colors.accent.copy(alpha = 0.12f)) {
                                    Text(
                                        stringResource(if (m.role == GroupRole.OWNER) R.string.talkwire_role_owner else R.string.talkwire_role_admin),
                                        fontSize = 11.sp,
                                        color = colors.accent,
                                        modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
                                    )
                                }
                            }
                        }
                        DropdownMenu(expanded = menuFor == m.uid, onDismissRequest = { menuFor = null }) {
                            if (perms.setRole) {
                                val toAdmin = m.role == GroupRole.MEMBER
                                DropdownMenuItem(
                                    text = { Text(stringResource(if (toAdmin) R.string.talkwire_make_admin else R.string.talkwire_remove_admin)) },
                                    onClick = {
                                        menuFor = null
                                        if (toAdmin && members.count { it.role == GroupRole.ADMIN } >= state.config.maxAdmins) {
                                            toast(R.string.talkwire_admin_limit)
                                        } else {
                                            act { engine.client.setMemberRole(id.id, m.uid, if (toAdmin) GroupRole.ADMIN else GroupRole.MEMBER) }
                                        }
                                    },
                                )
                            }
                            if (perms.transfer) {
                                DropdownMenuItem(text = { Text(stringResource(R.string.talkwire_transfer_owner)) }, onClick = { menuFor = null; pending = Pending.Transfer(m.uid) })
                            }
                            if (perms.kick) {
                                DropdownMenuItem(text = { Text(stringResource(R.string.talkwire_remove_member), color = colors.error) }, onClick = { menuFor = null; pending = Pending.Kick(m.uid) })
                            }
                        }
                    }
                }
                item {
                    Row(
                        Modifier.fillMaxWidth().background(colors.surface).clickable { state.navigate(ImDestination.PickMembers(id.id)) }.padding(horizontal = 16.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(Icons.Outlined.PersonAdd, null, tint = colors.accent, modifier = Modifier.size(24.dp))
                        Spacer(Modifier.width(12.dp))
                        Text(stringResource(R.string.talkwire_add_members), fontSize = 15.sp, color = colors.accent)
                    }
                    Text(stringResource(R.string.talkwire_members_private), fontSize = 12.sp, color = colors.textHint, modifier = Modifier.padding(16.dp))
                    if (myRole.canManage) {
                        SettingRow(stringResource(R.string.talkwire_rename_group)) { newName = title; pending = Pending.Rename }
                    }
                }
                state.host.groupInfoExtras?.let { extras -> item { extras(id.id, myRole) } }
            }
            item {
                Spacer(Modifier.height(8.dp))
                ToggleRow(stringResource(R.string.talkwire_pin_chat), summary?.pinned == true) { on -> scope.launch { runCatching { engine.setPinned(id, on) } } }
                ToggleRow(stringResource(R.string.talkwire_mute_notifications), summary?.muted == true) { on -> scope.launch { runCatching { engine.setMuted(id, on) } } }
                Spacer(Modifier.height(8.dp))
                // In groups only owners and admins clear for everyone; the server checks it too.
                if (!id.isGroup || myRole.canManage) SettingRow(stringResource(R.string.talkwire_clear_history), destructive = true) { pending = Pending.Clear }
                if (id.isGroup) {
                    if (myRole == GroupRole.OWNER) {
                        SettingRow(stringResource(R.string.talkwire_disband_group), destructive = true) { pending = Pending.Disband }
                    } else {
                        SettingRow(stringResource(R.string.talkwire_leave_group), destructive = true) { pending = Pending.Leave }
                    }
                }
                Spacer(Modifier.height(32.dp))
            }
        }
    }

    when (val p = pending) {
        null -> Unit
        is Pending.Kick -> Confirm(stringResource(R.string.talkwire_remove_confirm, name(p.uid)), stringResource(R.string.talkwire_remove_member), { pending = null }) {
            act { engine.client.kick(id.id, p.uid) }
        }
        is Pending.Transfer -> Confirm(stringResource(R.string.talkwire_transfer_confirm, name(p.uid)), stringResource(R.string.talkwire_transfer_owner), { pending = null }) {
            act { engine.client.transferOwner(id.id, p.uid) }
        }
        Pending.Leave -> Confirm(stringResource(R.string.talkwire_leave_confirm), stringResource(R.string.talkwire_leave_group), { pending = null }) {
            act(onDone = leaveChat) { engine.client.leaveGroup(id.id) }
        }
        Pending.Disband -> Confirm(stringResource(R.string.talkwire_disband_confirm), stringResource(R.string.talkwire_disband_group), { pending = null }) {
            act(onDone = leaveChat) { engine.client.disbandGroup(id.id) }
        }
        Pending.Clear -> Confirm(stringResource(R.string.talkwire_clear_history_confirm), stringResource(R.string.talkwire_clear_history), { pending = null }) {
            scope.launch {
                try {
                    engine.clear(id)
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    toast(R.string.talkwire_action_failed)
                }
            }
        }
        Pending.Rename -> ImAlertDialog(
            onDismissRequest = { pending = null },
            title = stringResource(R.string.talkwire_rename_group),
            confirmLabel = stringResource(R.string.talkwire_save),
            confirmEnabled = newName.isNotBlank(),
            onConfirm = {
                pending = null
                val n = newName.trim().take(GROUP_NAME_MAX)
                act(onDone = { runCatching { engine.refreshGroups() } }) { engine.client.renameGroup(id.id, n) }
            },
            dismissLabel = stringResource(R.string.talkwire_cancel),
            text = {
                Surface(shape = RoundedCornerShape(8.dp), color = colors.inputBackground) {
                    BasicTextField(
                        value = newName,
                        onValueChange = { newName = it.take(GROUP_NAME_MAX) },
                        singleLine = true,
                        textStyle = TextStyle(fontSize = 15.sp, color = colors.textPrimary),
                        cursorBrush = SolidColor(colors.accent),
                        modifier = Modifier.fillMaxWidth().padding(12.dp),
                    )
                }
            },
        )
    }
}

@Composable
private fun Confirm(message: String, action: String, dismiss: () -> Unit, run: () -> Unit) {
    ImConfirmDialog(
        message = message,
        confirmLabel = action,
        dismissLabel = stringResource(R.string.talkwire_cancel),
        destructive = true,
        onConfirm = { dismiss(); run() },
        onDismiss = dismiss,
    )
}

@Composable
private fun SettingRow(label: String, destructive: Boolean = false, onClick: () -> Unit) {
    val colors = ImTheme.colors
    Column(Modifier.background(colors.surface)) {
        Text(
            label,
            fontSize = 15.sp,
            color = if (destructive) colors.error else colors.textPrimary,
            modifier = Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 14.dp),
        )
        Divider(color = colors.divider, thickness = 0.5.dp, modifier = Modifier.padding(start = 16.dp))
    }
}

@Composable
private fun ToggleRow(label: String, on: Boolean, onChange: (Boolean) -> Unit) {
    val colors = ImTheme.colors
    Row(Modifier.fillMaxWidth().background(colors.surface).padding(horizontal = 16.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(label, fontSize = 15.sp, color = colors.textPrimary, modifier = Modifier.weight(1f))
        Switch(checked = on, onCheckedChange = onChange, colors = SwitchDefaults.colors(checkedTrackColor = colors.accent))
    }
}
