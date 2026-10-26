package io.talkwire.android.crypto

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import io.talkwire.core.model.ChatMessage
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.MessageStatus
import io.talkwire.storage.room.RoomChatStore
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import javax.crypto.AEADBadTagException
import javax.crypto.spec.SecretKeySpec

/**
 * The wrapping logic with a software key standing in for the Keystore key:
 * Robolectric has no AndroidKeyStore provider, so the Keystore itself is only
 * exercised on a device.
 */
@RunWith(RobolectricTestRunner::class)
class AndroidKeystoreKeyWrapperTest {
    private val kek = SecretKeySpec(ByteArray(32) { (it * 7).toByte() }, "AES")
    private val wrapper = AndroidKeystoreKeyWrapper { kek }

    @Test
    fun wrapsAndUnwrapsWithAFreshIvEachTime() {
        val dataKey = ByteArray(32) { it.toByte() }
        val a = wrapper.wrap(dataKey)
        val b = wrapper.wrap(dataKey)
        assertFalse(a.iv.contentEquals(b.iv))
        assertFalse(a.ciphertext.contentEquals(dataKey))
        assertArrayEquals(dataKey, wrapper.unwrap(a))
        assertArrayEquals(dataKey, wrapper.unwrap(b))
    }

    @Test(expected = AEADBadTagException::class)
    fun anotherKeyCannotUnwrap() {
        val wrapped = wrapper.wrap(ByteArray(32))
        AndroidKeystoreKeyWrapper { SecretKeySpec(ByteArray(32), "AES") }.unwrap(wrapped)
    }

    @Test
    fun sealsTheRoomStore() = runBlocking {
        val store = RoomChatStore.inMemory(ApplicationProvider.getApplicationContext<Context>(), wrapper)
        try {
            val c = ConversationId("uid_aaaaaaaaaaa")
            store.putMessage(ChatMessage(c, 1, null, "uid_bbbbbbbbbbb", "sealed", "text", ts = 1, status = MessageStatus.RECEIVED))
            assertEquals("sealed", store.message(c, 1)!!.content)
        } finally {
            store.close()
        }
    }
}
