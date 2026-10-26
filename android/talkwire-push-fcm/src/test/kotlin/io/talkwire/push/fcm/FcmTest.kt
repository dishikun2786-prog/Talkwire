package io.talkwire.push.fcm

import com.google.android.gms.tasks.TaskCompletionSource
import com.google.android.gms.tasks.Tasks
import com.google.firebase.messaging.RemoteMessage
import io.talkwire.core.notify.PushNotice
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.io.IOException

@RunWith(RobolectricTestRunner::class)
class FcmTest {
    private class Recorder : TalkwireMessagingService() {
        val notices = mutableListOf<PushNotice>()
        val others = mutableListOf<RemoteMessage>()
        val tokens = mutableListOf<String>()

        override fun onImPush(notice: PushNotice) {
            notices += notice
        }

        override fun onOtherMessage(message: RemoteMessage) {
            others += message
        }

        override fun onTokenChanged(token: String) {
            tokens += token
        }
    }

    private fun push(data: Map<String, String>) = RemoteMessage.Builder("123@fcm.googleapis.com").setData(data).build()

    @Test
    fun dataPushesBecomeNotices() {
        val s = Recorder()
        s.onMessageReceived(push(mapOf("topic" to "grp_abcdefabcdef", "isGroup" to "true", "groupName" to "Team", "senderName" to "Bob", "preview" to "hi")))
        s.onMessageReceived(push(mapOf("topic" to "uid_abcdefghijk", "isGroup" to "false", "type" to "e2e_message", "e2e" to "true")))
        s.onMessageReceived(push(mapOf("campaign" to "spring")))
        assertEquals(2, s.notices.size)
        val group = s.notices[0]
        assertEquals(PushNotice.Kind.MESSAGE, group.kind)
        assertTrue(group.isGroup)
        assertEquals("Team", group.groupName)
        assertEquals("hi", group.preview)
        assertEquals(PushNotice.Kind.E2E_MESSAGE, s.notices[1].kind)
        assertNull("no preview for encrypted messages", s.notices[1].preview)
        assertEquals(1, s.others.size)
    }

    @Test
    fun tokenChangesAreForwarded() {
        val s = Recorder()
        s.onNewToken("t-1")
        assertEquals(listOf("t-1"), s.tokens)
    }

    @Test
    fun tokenSource() = runBlocking {
        assertEquals("fcm", FcmTokenSource { Tasks.forResult("tok") }.provider)
        assertEquals("tok", FcmTokenSource { Tasks.forResult("tok") }.token())
        assertNull("no Play services: no token", FcmTokenSource { Tasks.forException(IOException("SERVICE_NOT_AVAILABLE")) }.token())
        assertNull(FcmTokenSource { throw IllegalStateException("Default FirebaseApp is not initialized") }.token())
        val never = TaskCompletionSource<String>()
        val timedOut = try {
            withTimeout(200) { FcmTokenSource { never.task }.token() }
            false
        } catch (e: TimeoutCancellationException) {
            true
        }
        assertTrue("waiting is cancellable", timedOut)
    }
}
