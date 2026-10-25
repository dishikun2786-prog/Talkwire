package io.imkit.storage.room

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import io.imkit.core.store.ChatStore
import io.imkit.core.store.ChatStoreContract
import org.junit.After
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/** The store contract on Room, without sealing. */
@RunWith(RobolectricTestRunner::class)
class RoomChatStoreContractTest : ChatStoreContract() {
    private val opened = mutableListOf<RoomChatStore>()

    override fun newStore(): ChatStore =
        RoomChatStore.inMemory(ApplicationProvider.getApplicationContext<Context>()).also { opened += it }

    @After
    fun closeAll() = opened.forEach { it.close() }
}

/** The same contract with every text field sealed: sealing must be invisible to the engine. */
@RunWith(RobolectricTestRunner::class)
class SealedRoomChatStoreContractTest : ChatStoreContract() {
    private val opened = mutableListOf<RoomChatStore>()

    override fun newStore(): ChatStore =
        RoomChatStore.inMemory(ApplicationProvider.getApplicationContext<Context>(), TestKeyWrapper()).also { opened += it }

    @After
    fun closeAll() = opened.forEach { it.close() }
}
