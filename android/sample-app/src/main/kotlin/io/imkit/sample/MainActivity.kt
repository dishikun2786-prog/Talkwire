package io.imkit.sample

import android.Manifest
import android.app.Application
import android.content.Intent
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import android.widget.Toast
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedTextField
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.imkit.call.CallManager
import io.imkit.call.CallScreen
import io.imkit.call.CallState
import io.imkit.core.model.ConversationId
import io.imkit.groupjoin.JoinGroupScreen
import io.imkit.groupjoin.groupJoinNotice
import io.imkit.ui.host.ImDeepLink
import io.imkit.ui.screens.ImBannerHost
import io.imkit.ui.screens.ImChatApp
import io.imkit.ui.screens.ImDestination
import io.imkit.ui.screens.rememberImChatState
import io.imkit.ui.theme.ImTheme
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient

/** Holds the session for the process, so a rotation or a notification tap reuses it. */
class SampleApp : Application() {
    val http: OkHttpClient by lazy { OkHttpClient() }
    val accounts: DevAccount by lazy { DevAccount(this, http) }
    var session: ChatSession? = null
        private set

    fun sessionFor(account: Account): ChatSession {
        session?.let { if (it.account == account) return it else it.close() }
        return ChatSession(this, account, http).also { session = it; it.start() }
    }

    fun signOut() {
        session?.close()
        session = null
        accounts.signOut()
    }
}

class MainActivity : ComponentActivity() {
    private val links = MutableStateFlow<ImDeepLink?>(null)

    /** A QR token to join with, from an `imkitsample://join/` link. */
    private val joins = MutableStateFlow<String?>(null)

    private fun handle(intent: Intent?) {
        val data = intent?.data ?: return
        if (data.scheme == "imkitsample") {
            qrTokenOf(data.toString())?.let { joins.value = it }
        } else {
            ImDeepLink.parse(data)?.let { links.value = it }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        handle(intent)
        val app = application as SampleApp
        setContent {
            var account by remember { mutableStateOf(app.accounts.current()) }
            ImTheme {
                Surface(Modifier.fillMaxSize()) {
                    val a = account
                    if (a == null) {
                        SignIn(app) { account = it }
                    } else {
                        Chat(app.sessionFor(a), onSignOut = { app.signOut(); account = null })
                    }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handle(intent)
    }

    override fun onStart() {
        super.onStart()
        (application as SampleApp).session?.notifications?.appInForeground = true
    }

    override fun onStop() {
        super.onStop()
        (application as SampleApp).session?.notifications?.appInForeground = false
    }

    @Composable
    private fun Chat(session: ChatSession, onSignOut: () -> Unit) {
        val state = rememberImChatState(session.engine, session.host, session.types)
        LaunchedEffect(state) {
            session.notifications.appInForeground = true
            state.openConversation.collect { session.notifications.openConversation = it }
        }
        LaunchedEffect(state) {
            links.collect { link -> if (link != null) { state.open(link); links.value = null } }
        }
        var joining by remember { mutableStateOf<String?>(null) }
        var askJoin by remember { mutableStateOf(false) }
        LaunchedEffect(Unit) {
            joins.collect { token -> if (token != null) { joining = token; joins.value = null } }
        }
        val context = LocalContext.current
        val nameOf = { uid: String -> session.host.contacts.value.firstOrNull { it.uid == uid }?.name }
        LaunchedEffect(session) {
            session.groupJoin.events.collect { e ->
                Toast.makeText(context, groupJoinNotice(context, e) { uid -> nameOf(uid) ?: uid }, Toast.LENGTH_LONG).show()
            }
        }
        AskForNotifications()
        ShowOverLockScreenInCalls(session.calls)
        Box(Modifier.fillMaxSize().safeDrawingPadding()) {
            ImChatApp(state, Modifier.fillMaxSize())
            ImBannerHost(session.banners, state, onOpen = { state.open(ImDeepLink.Conversation(it)) }, modifier = Modifier.align(Alignment.TopCenter))
            session.redPacketSender.Dialog()
            // Only on the conversation list: elsewhere the bottom-left corner is the composer's mic button.
            if (state.current is ImDestination.Conversations && joining == null) {
                Row(Modifier.align(Alignment.BottomStart).padding(4.dp)) {
                    TextButton(onClick = onSignOut) {
                        Text(stringResource(R.string.sample_sign_out) + " (" + session.account.name + ")", fontSize = 11.sp)
                    }
                    TextButton(onClick = { askJoin = true }, modifier = Modifier.testTag("sample_join")) {
                        Text(stringResource(R.string.sample_join_group), fontSize = 11.sp)
                    }
                }
            }
            joining?.let { token ->
                Surface(Modifier.fillMaxSize()) {
                    Column(Modifier.fillMaxSize()) {
                        TextButton(onClick = { joining = null }) { Text(stringResource(R.string.sample_close)) }
                        JoinGroupScreen(
                            session.groupJoin,
                            session.engine,
                            token,
                            Modifier.weight(1f),
                            onOpen = { topic ->
                                joining = null
                                state.open(ImDeepLink.Conversation(ConversationId.group(topic)))
                            },
                        )
                    }
                }
            }
            // Over everything while a call rings, runs or has just ended.
            CallScreen(
                session.calls,
                Modifier.fillMaxSize(),
                nameOf = nameOf,
                avatarOf = { uid -> session.host.resolveMediaUrl(session.host.contacts.value.firstOrNull { it.uid == uid }?.avatarUrl) },
            )
        }
        if (askJoin) JoinDialog(onDismiss = { askJoin = false }, onJoin = { joining = it; askJoin = false })
    }

    /** Lets the call screen show over the lock screen, and wake it, while a call rings or runs. */
    @Composable
    private fun ShowOverLockScreenInCalls(calls: CallManager) {
        val call by calls.state.collectAsState()
        val inCall = call is CallState.Incoming || call is CallState.Active
        LaunchedEffect(inCall) {
            if (Build.VERSION.SDK_INT >= 27) {
                setShowWhenLocked(inCall)
                setTurnScreenOn(inCall)
            }
        }
    }

    @Composable
    private fun AskForNotifications() {
        if (Build.VERSION.SDK_INT < 33) return
        val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
        LaunchedEffect(Unit) { ask.launch(Manifest.permission.POST_NOTIFICATIONS) }
    }
}

@Composable
private fun SignIn(app: SampleApp, onSignedIn: (Account) -> Unit) {
    var name by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    Column(
        Modifier.fillMaxSize().safeDrawingPadding().padding(24.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(stringResource(R.string.sample_title), fontSize = 22.sp)
        Spacer(Modifier.height(8.dp))
        Text(stringResource(R.string.sample_dev_hint), fontSize = 13.sp)
        Spacer(Modifier.height(24.dp))
        OutlinedTextField(
            value = name,
            onValueChange = { name = it.filter { c -> c.isLetterOrDigit() || c == '_' }.take(20) },
            label = { Text(stringResource(R.string.sample_name)) },
            singleLine = true,
            modifier = Modifier.fillMaxWidth().testTag("sample_name"),
        )
        Spacer(Modifier.height(16.dp))
        Button(
            enabled = name.length >= 2 && !busy,
            onClick = {
                busy = true
                error = null
                scope.launch {
                    runCatching { app.accounts.signIn(name) }
                        .onSuccess(onSignedIn)
                        .onFailure { error = it.message ?: it.toString() }
                    busy = false
                }
            },
            modifier = Modifier.fillMaxWidth().testTag("sample_sign_in"),
        ) { Text(stringResource(R.string.sample_sign_in)) }
        error?.let {
            Spacer(Modifier.height(12.dp))
            Text(it, color = androidx.compose.ui.graphics.Color(0xFFD32F2F), fontSize = 13.sp)
        }
    }
}

/** Asks for a group link or code; the join screen takes it from there. */
@Composable
private fun JoinDialog(onDismiss: () -> Unit, onJoin: (String) -> Unit) {
    var text by remember { mutableStateOf("") }
    var bad by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.sample_join_group)) },
        text = {
            Column {
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it; bad = false },
                    label = { Text(stringResource(R.string.sample_join_hint)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().testTag("sample_join_link"),
                )
                if (bad) Text(stringResource(R.string.sample_join_bad_link), color = androidx.compose.ui.graphics.Color(0xFFD32F2F), fontSize = 13.sp)
            }
        },
        confirmButton = {
            TextButton(onClick = { qrTokenOf(text)?.let(onJoin) ?: run { bad = true } }, modifier = Modifier.testTag("sample_join_open")) {
                Text(stringResource(R.string.sample_join_open))
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.sample_cancel)) } },
    )
}
