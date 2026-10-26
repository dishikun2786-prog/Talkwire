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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import io.talkwire.core.client.createGroup
import io.talkwire.core.client.invite
import io.talkwire.core.model.ConversationId
import io.talkwire.ui.R
import io.talkwire.ui.components.ImAvatar
import io.talkwire.ui.host.ImProfile
import io.talkwire.ui.theme.ImTheme
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** The server's limit on group names (`group_chat.name_max_len`). */
internal const val GROUP_NAME_MAX = 30

/** A default name for a new group: the members' names, cut to fit. */
internal fun defaultGroupName(names: List<String>): String {
    val joined = names.joinToString(", ")
    return if (joined.length <= GROUP_NAME_MAX) joined else joined.take(GROUP_NAME_MAX - 1).trimEnd(',', ' ') + "…"
}

@Composable
internal fun SimpleTopBar(title: String, onBack: () -> Unit, action: (@Composable () -> Unit)? = null) {
    val colors = ImTheme.colors
    Surface(color = colors.background) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 4.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack) { Icon(Icons.Filled.ArrowBack, stringResource(R.string.talkwire_back), tint = colors.textPrimary) }
            Text(title, fontSize = 17.sp, fontWeight = FontWeight.Medium, color = colors.textPrimary, maxLines = 1, modifier = Modifier.weight(1f))
            action?.invoke()
        }
    }
}

@Composable
internal fun SearchField(value: String, tag: String = "talkwire_search", onChange: (String) -> Unit) {
    val colors = ImTheme.colors
    Surface(shape = RoundedCornerShape(8.dp), color = colors.inputBackground, modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp)) {
        BasicTextField(
            value = value,
            onValueChange = onChange,
            singleLine = true,
            textStyle = TextStyle(fontSize = 15.sp, color = colors.textPrimary),
            cursorBrush = SolidColor(colors.accent),
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 9.dp).testTag(tag),
            decorationBox = { inner ->
                if (value.isEmpty()) Text(stringResource(R.string.talkwire_search), color = colors.textHint, fontSize = 15.sp)
                inner()
            },
        )
    }
}

@Composable
private fun PersonRow(p: ImProfile, avatarUrl: String?, checked: Boolean?, enabled: Boolean = true, onClick: () -> Unit) {
    val colors = ImTheme.colors
    Row(
        Modifier.fillMaxWidth().clickable(enabled = enabled, onClick = onClick).padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (checked != null) {
            Checkbox(checked = checked, onCheckedChange = { onClick() }, enabled = enabled)
            Spacer(Modifier.width(4.dp))
        }
        ImAvatar(avatarUrl, p.name, size = 40.dp)
        Spacer(Modifier.width(12.dp))
        Text(p.name, fontSize = 15.sp, color = if (enabled) colors.textPrimary else colors.textHint, maxLines = 1)
    }
}

/** Contacts from the host; a tap opens (or starts) the chat. */
@Composable
internal fun ContactsScreen(state: ImChatState, onBack: () -> Unit) {
    val colors = ImTheme.colors
    val contacts by state.host.contacts.collectAsState()
    var query by remember { mutableStateOf("") }
    val scope = rememberCoroutineScope()
    LaunchedEffect(Unit) { runCatching { state.host.refreshContacts() } }
    val shown = remember(contacts, query) { contacts.filter { query.isBlank() || it.name.contains(query.trim(), ignoreCase = true) }.sortedBy { it.name.lowercase() } }
    Column(Modifier.fillMaxSize().background(colors.background)) {
        SimpleTopBar(stringResource(R.string.talkwire_contacts), onBack)
        SearchField(query) { query = it }
        if (shown.isEmpty()) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { Text(stringResource(R.string.talkwire_no_contacts), color = colors.textSecondary) }
        } else {
            LazyColumn(Modifier.fillMaxSize().testTag("talkwire_contacts")) {
                items(shown, key = { it.uid }) { p ->
                    PersonRow(p, state.host.resolveMediaUrl(p.avatarUrl), checked = null) {
                        val id = ConversationId.direct(p.uid)
                        scope.launch { runCatching { state.engine.ensureConversation(id, p.name) } }
                        state.replace(ImDestination.Chat(id))
                    }
                }
            }
        }
    }
}

/** Picks contacts for a new group, or to add to [topic]. */
@Composable
internal fun PickMembersScreen(state: ImChatState, topic: String?, onBack: () -> Unit) {
    val colors = ImTheme.colors
    val context = LocalContext.current
    val contacts by state.host.contacts.collectAsState()
    var query by remember { mutableStateOf("") }
    var picked by remember { mutableStateOf(emptySet<String>()) }
    var existing by remember { mutableStateOf(emptySet<String>()) }
    var groupName by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(Unit) { runCatching { state.host.refreshContacts() } }
    if (topic != null) LaunchedEffect(topic) { existing = state.engine.members(topic, refresh = true)?.map { it.uid }?.toSet().orEmpty() }
    val shown = remember(contacts, query) { contacts.filter { it.uid != state.engine.me && (query.isBlank() || it.name.contains(query.trim(), ignoreCase = true)) }.sortedBy { it.name.lowercase() } }

    fun submit() {
        if (picked.isEmpty() || busy) return
        busy = true
        scope.launch {
            try {
                if (topic == null) {
                    val names = contacts.filter { it.uid in picked }.map { it.name }
                    val name = groupName.trim().ifEmpty { defaultGroupName(names) }.take(GROUP_NAME_MAX)
                    val created = state.engine.client.createGroup(name, picked.toList())
                    runCatching { state.engine.refreshGroups() }
                    state.replace(ImDestination.Chat(ConversationId.group(created)))
                } else {
                    val failed = picked.count { uid -> !runCatching { state.engine.client.invite(topic, uid).ok }.getOrDefault(false) }
                    runCatching { state.engine.members(topic, refresh = true) }
                    if (failed > 0) Toast.makeText(context, context.getString(R.string.talkwire_action_failed), Toast.LENGTH_SHORT).show()
                    onBack()
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Toast.makeText(context, context.getString(R.string.talkwire_action_failed), Toast.LENGTH_SHORT).show()
            } finally {
                busy = false
            }
        }
    }

    Column(Modifier.fillMaxSize().background(colors.background)) {
        SimpleTopBar(stringResource(if (topic == null) R.string.talkwire_create_group else R.string.talkwire_add_members), onBack) {
            if (busy) {
                CircularProgressIndicator(Modifier.padding(12.dp).width(20.dp), strokeWidth = 2.dp)
            } else {
                TextButton(onClick = ::submit, enabled = picked.isNotEmpty(), modifier = Modifier.testTag("talkwire_pick_done")) {
                    Text(
                        stringResource(if (topic == null) R.string.talkwire_create_count else R.string.talkwire_add_count, picked.size),
                        color = if (picked.isNotEmpty()) colors.accent else colors.textHint,
                    )
                }
            }
        }
        if (topic == null) {
            Surface(shape = RoundedCornerShape(8.dp), color = colors.inputBackground, modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp)) {
                BasicTextField(
                    value = groupName,
                    onValueChange = { groupName = it.take(GROUP_NAME_MAX) },
                    singleLine = true,
                    textStyle = TextStyle(fontSize = 15.sp, color = colors.textPrimary),
                    cursorBrush = SolidColor(colors.accent),
                    modifier = Modifier.padding(horizontal = 12.dp, vertical = 9.dp).testTag("talkwire_group_name"),
                    decorationBox = { inner ->
                        if (groupName.isEmpty()) Text(stringResource(R.string.talkwire_group_name), color = colors.textHint, fontSize = 15.sp)
                        inner()
                    },
                )
            }
        }
        SearchField(query) { query = it }
        if (shown.isEmpty()) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { Text(stringResource(R.string.talkwire_no_contacts), color = colors.textSecondary) }
        } else {
            LazyColumn(Modifier.fillMaxSize()) {
                items(shown, key = { it.uid }) { p ->
                    val already = p.uid in existing
                    PersonRow(p, state.host.resolveMediaUrl(p.avatarUrl), checked = already || p.uid in picked, enabled = !already) {
                        picked = if (p.uid in picked) picked - p.uid else picked + p.uid
                    }
                }
            }
        }
    }
}
