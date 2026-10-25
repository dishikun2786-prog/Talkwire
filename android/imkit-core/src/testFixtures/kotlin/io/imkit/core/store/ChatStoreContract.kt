package io.imkit.core.store

import io.imkit.core.json.Json
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.MessageStatus
import io.imkit.core.model.SendFailure
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What every [ChatStore] must do, as the engine relies on it. Subclass it
 * with [newStore] to check an implementation; [InMemoryChatStore] and the
 * Room store in `imkit-storage-room` both run it.
 */
abstract class ChatStoreContract {
    /** A fresh, empty store. */
    abstract fun newStore(): ChatStore

    protected val a = ConversationId("uid_aaaaaaaaaaa")
    protected val g = ConversationId("grp_gggggggggggg")
    protected val me = "uid_meeeeeeeeee"
    protected val peer = "uid_aaaaaaaaaaa"

    protected fun stored(c: ConversationId, seq: Long, from: String = peer, content: String = "m$seq", contentType: String = "text") =
        ChatMessage(c, seq, null, from, content, contentType, ts = 1_000 + seq, status = MessageStatus.RECEIVED)

    protected fun pending(c: ConversationId, id: String, ts: Long = 5_000, status: MessageStatus = MessageStatus.SENDING) =
        ChatMessage(c, null, id, me, "pending $id", "text", ts = ts, status = status)

    private fun run(block: suspend ChatStore.() -> Unit) = runBlocking { newStore().block() }

    @Test
    fun ownerIsClaimedAndWipedWithEverything() = run {
        assertNull(owner())
        setOwner(me)
        assertEquals(me, owner())
        putMessage(stored(a, 1))
        putMessage(pending(a, "p1"))
        addRecalled(a, 2)
        setClearState(a, ClearState(1, 0))
        setReadCursor(g, peer, 3)
        putConversation(StoredConversation(a, draft = "d"))
        wipe()
        assertNull(owner())
        assertTrue(seqs(a).isEmpty())
        assertTrue(pendingMessages().isEmpty())
        assertTrue(recalledSeqs(a).isEmpty())
        assertNull(clearState(a))
        assertTrue(readCursors(g).isEmpty())
        assertTrue(conversations().isEmpty())
    }

    @Test
    fun everyMessageFieldSurvivesTheRoundTrip() = run {
        val full = ChatMessage(
            conversation = g,
            seq = 7,
            localId = "cid-7",
            from = me,
            content = "hello\nwith ünïcödé and 'quotes'",
            contentType = "image",
            fileUrl = "https://example.com/a.png",
            fileName = "a.png",
            duration = 12,
            extra = Json.parse("""{"w":640,"h":480,"tags":["x",1,true,null]}"""),
            senderKind = ChatMessage.SENDER_AI,
            forwardFromMsgId = 3,
            forwardFromTopic = "grp_ffffffffffff",
            ts = 1_234_567,
            status = MessageStatus.SENT,
        )
        putMessage(full)
        assertEquals(full, message(g, 7))
        val failed = pending(g, "f1", status = MessageStatus.FAILED).copy(failure = SendFailure(403, "not_member", "not a member"))
        putMessage(failed)
        assertEquals(failed, pendingMessage(g, "f1"))
        val noReason = pending(g, "f2", status = MessageStatus.FAILED).copy(failure = SendFailure(0, null, null))
        putMessage(noReason)
        assertEquals(noReason, pendingMessage(g, "f2"))
    }

    @Test
    fun putReplacesByKey() = run {
        putMessage(stored(a, 1, content = "old"))
        putMessage(stored(a, 1, content = "new"))
        assertEquals("new", message(a, 1)?.content)
        assertEquals(listOf(1L), seqs(a))
        putMessage(pending(a, "p").copy(content = "first"))
        putMessage(pending(a, "p").copy(content = "second"))
        assertEquals(listOf("second"), pendingMessages().map { it.content })
    }

    @Test
    fun promoteMovesAPendingRowToItsSeq() = run {
        putMessage(pending(a, "p1"))
        promote(a, "p1", pending(a, "p1").copy(seq = 4, status = MessageStatus.SENT, ts = 2_000))
        assertNull(pendingMessage(a, "p1"))
        assertEquals("p1", message(a, 4)?.localId)
        assertEquals(MessageStatus.SENT, message(a, 4)?.status)
        assertTrue(pendingMessages().isEmpty())
    }

    @Test
    fun latestIsTheNewestStoredThenEveryPendingInTimelineOrder() = run {
        for (s in 1L..6L) putMessage(stored(a, s))
        putMessage(pending(a, "late", ts = 9_000))
        putMessage(pending(a, "early", ts = 8_000))
        putMessage(stored(g, 99)) // another conversation
        val latest = latestMessages(a, 3)
        assertEquals(listOf("4", "5", "6", "early", "late"), latest.map { it.seq?.toString() ?: it.localId })
        assertEquals(listOf(2L, 3L), messagesBefore(a, 4, 2).map { it.seq })
        assertEquals(listOf(1L, 2L, 3L), messagesBefore(a, 4, 10).map { it.seq })
        assertTrue(messagesBefore(a, 1, 10).isEmpty())
        assertEquals((1L..6L).toList(), seqs(a))
    }

    @Test
    fun pendingMessagesSpanConversations() = run {
        putMessage(pending(a, "x"))
        putMessage(pending(g, "y", status = MessageStatus.FAILED))
        putMessage(stored(a, 1))
        assertEquals(setOf("x", "y"), pendingMessages().map { it.localId }.toSet())
    }

    @Test
    fun deletesAreScoped() = run {
        for (s in 1L..5L) putMessage(stored(a, s))
        putMessage(pending(a, "p"))
        putMessage(stored(g, 1))
        deleteMessage(a, 2)
        assertEquals(listOf(1L, 3L, 4L, 5L), seqs(a))
        deleteThrough(a, 3)
        assertEquals(listOf(4L, 5L), seqs(a))
        assertEquals("partial clear keeps pending", 1, pendingMessages().size)
        deletePending(a, "p")
        assertTrue(pendingMessages().isEmpty())
        putMessage(pending(a, "q"))
        addRecalled(a, 9)
        deleteAllMessages(a)
        assertTrue(seqs(a).isEmpty())
        assertTrue(pendingMessages().isEmpty())
        assertTrue(recalledSeqs(a).isEmpty())
        assertEquals("other conversations untouched", listOf(1L), seqs(g))
    }

    @Test
    fun unreadCountsOthersNonSystemMessagesAfterTheReadPosition() = run {
        putMessage(stored(a, 1))
        putMessage(stored(a, 2, from = me))
        putMessage(stored(a, 3))
        putMessage(stored(a, 4, contentType = "system"))
        putMessage(stored(a, 5))
        assertEquals(3, countUnread(a, 0, me))
        assertEquals(1, countUnread(a, 3, me))
        assertEquals(0, countUnread(a, 5, me))
    }

    @Test
    fun recallTombstonesClearStateAndCursors() = run {
        addRecalled(a, 5)
        addRecalled(a, 2)
        addRecalled(a, 5)
        assertEquals(listOf(2L, 5L), recalledSeqs(a))
        assertNull(clearState(a))
        setClearState(a, ClearState(1, 10))
        setClearState(a, ClearState(2, 20))
        assertEquals(ClearState(2, 20), clearState(a))
        setReadCursor(g, peer, 3)
        setReadCursor(g, me, 4)
        setReadCursor(g, peer, 6)
        assertEquals(mapOf(peer to 6L, me to 4L), readCursors(g))
    }

    @Test
    fun conversationsRoundTripAndDeleteTakesEverything() = run {
        val conv = StoredConversation(g, title = "team", lastReadSeq = 3, peerReadSeq = 2, draft = "draft text", pinned = true, muted = true, left = true, updatedAt = 77)
        putConversation(conv)
        putConversation(StoredConversation(a))
        assertEquals(conv, conversation(g))
        assertEquals(setOf(a, g), conversations().map { it.id }.toSet())
        putConversation(conv.copy(draft = null, left = false))
        assertEquals(conv.copy(draft = null, left = false), conversation(g))
        putMessage(stored(g, 1))
        putMessage(pending(g, "p"))
        addRecalled(g, 2)
        setClearState(g, ClearState(1, 0))
        setReadCursor(g, peer, 1)
        putMessage(stored(a, 1))
        deleteConversation(g)
        assertNull(conversation(g))
        assertTrue(seqs(g).isEmpty())
        assertTrue(pendingMessages().isEmpty())
        assertTrue(recalledSeqs(g).isEmpty())
        assertNull(clearState(g))
        assertTrue(readCursors(g).isEmpty())
        assertEquals(listOf(1L), seqs(a))
        assertEquals(listOf(a), conversations().map { it.id })
    }
}
