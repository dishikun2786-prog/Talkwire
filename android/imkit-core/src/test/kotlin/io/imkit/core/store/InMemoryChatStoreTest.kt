package io.imkit.core.store

class InMemoryChatStoreTest : ChatStoreContract() {
    override fun newStore(): ChatStore = InMemoryChatStore()
}
