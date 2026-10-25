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
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
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

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        links.value = intent?.data?.let(ImDeepLink::parse)
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
        intent.data?.let(ImDeepLink::parse)?.let { links.value = it }
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
        AskForNotifications()
        Box(Modifier.fillMaxSize().safeDrawingPadding()) {
            ImChatApp(state, Modifier.fillMaxSize())
            ImBannerHost(session.banners, state, onOpen = { state.open(ImDeepLink.Conversation(it)) }, modifier = Modifier.align(Alignment.TopCenter))
            session.redPacketSender.Dialog()
            // Only on the conversation list: elsewhere the bottom-left corner is the composer's mic button.
            if (state.current is ImDestination.Conversations) {
                TextButton(onClick = onSignOut, modifier = Modifier.align(Alignment.BottomStart).padding(4.dp)) {
                    Text(stringResource(R.string.sample_sign_out) + " (" + session.account.name + ")", fontSize = 11.sp)
                }
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
