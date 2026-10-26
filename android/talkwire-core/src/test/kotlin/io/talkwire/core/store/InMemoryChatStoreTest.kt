package io.talkwire.core.store

class InMemoryChatStoreTest : ChatStoreContract() {
    override fun newStore(): ChatStore = InMemoryChatStore()
}
