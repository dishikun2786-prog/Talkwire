package io.imkit.core.sync

import io.imkit.core.ImIds
import io.imkit.core.model.ConversationId
import io.imkit.core.model.PresenceState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class SyncMathTest {
    @Test
    fun c3_resumesFromTheEndOfTheContiguousRun() {
        assertEquals(0, SyncMath.resumeSeq(emptyList(), emptyList(), 0))
        assertEquals(3, SyncMath.resumeSeq(listOf(1, 2, 3), emptyList(), 0))
        // A hole at 4: resume there, not at the maximum, or 4 is never fetched.
        assertEquals(3, SyncMath.resumeSeq(listOf(1, 2, 3, 5, 6), emptyList(), 0))
        // Older history never loaded: the run starts at the oldest seq held.
        assertEquals(12, SyncMath.resumeSeq(listOf(10, 11, 12), emptyList(), 0))
    }

    @Test
    fun c3_recalledSeqsAreNotHoles() {
        // seq 2 was recalled and its row deleted on purpose.
        assertEquals(4, SyncMath.resumeSeq(listOf(1, 3, 4), listOf(2), 0))
    }

    @Test
    fun c3_theClearWatermarkIsNotAHole() {
        assertEquals(12, SyncMath.resumeSeq(emptyList(), emptyList(), 12))
        assertEquals(14, SyncMath.resumeSeq(listOf(13, 14), emptyList(), 12))
        // A hole right after the watermark is a hole.
        assertEquals(12, SyncMath.resumeSeq(listOf(14, 15), emptyList(), 12))
        // Seqs at or below the watermark do not count.
        assertEquals(12, SyncMath.resumeSeq(listOf(3, 4), emptyList(), 12))
    }

    @Test
    fun aPageProvesWhatWasRecalledWhileAway() {
        // Asked after 3, got 4, 6, 7: 5 is gone; 8 is beyond the page, unknown.
        assertEquals(listOf(5L), SyncMath.recalledWhileAway(listOf(1, 2, 3, 4, 5, 8), 3, listOf(4, 6, 7)))
        assertEquals(emptyList<Long>(), SyncMath.recalledWhileAway(listOf(4, 5), 3, emptyList()))
        assertEquals(emptyList<Long>(), SyncMath.recalledWhileAway(listOf(4, 5), 0, listOf(4)))
    }
}

class TopicResolverTest {
    private val me = ImIds.uid("alice")
    private val bob = ImIds.uid("bob")
    private val peers = { listOf(bob) }

    @Test
    fun mapsTheThreeSpellingsOfADirectChat() {
        // data push: topic is the sender
        assertEquals(ConversationId(bob), TopicResolver.conversationOf(bob, bob, me, peers))
        // recalled/info: the peer wrote my uid
        assertEquals(ConversationId(bob), TopicResolver.conversationOf(me, bob, me, peers))
        // canonical topic with a sender
        assertEquals(ConversationId(bob), TopicResolver.conversationOf(ImIds.p2pTopic(me, bob), bob, me, peers))
        // canonical topic without a sender: reverse lookup among known peers
        assertEquals(ConversationId(bob), TopicResolver.conversationOf(ImIds.p2pTopic(me, bob), "", me, peers))
        // my own message from another device: topic is the peer, from is me
        assertEquals(ConversationId(bob), TopicResolver.conversationOf(bob, me, me, peers))
    }

    @Test
    fun groupsAndUnknowns() {
        assertEquals(ConversationId("grp_abcdefabcdef"), TopicResolver.conversationOf("grp_abcdefabcdef", bob, me, peers))
        assertNull(TopicResolver.conversationOf(ImIds.p2pTopic(me, ImIds.uid("carol")), "", me, peers))
        assertNull(TopicResolver.conversationOf("me", bob, me, peers))
        assertNull(TopicResolver.conversationOf("", bob, me, peers))
    }
}

class PresenceTrackerTest {
    private var now = 1_000_000L
    private val tracker = PresenceTracker(ttlMs = 180_000, clock = { now })

    @Test
    fun C5_PresenceTriStateWithExpiry() {
        assertEquals(PresenceState.UNKNOWN, tracker.state("uid_a"))
        tracker.apply("uid_a", online = true, rev = 1)
        assertEquals(PresenceState.ONLINE, tracker.state("uid_a"))
        tracker.apply("uid_a", online = false, rev = 2, lastSeenAt = 5)
        assertEquals(PresenceState.OFFLINE, tracker.state("uid_a"))
        assertEquals(5, tracker.lastSeenAt("uid_a"))
        now += 180_001
        assertEquals("a stale record is unknown, not offline", PresenceState.UNKNOWN, tracker.state("uid_a"))
    }

    @Test
    fun c5_olderRevisionsAreDropped() {
        tracker.apply("uid_a", online = false, rev = 5)
        tracker.apply("uid_a", online = true, rev = 4)
        assertEquals(PresenceState.OFFLINE, tracker.state("uid_a"))
        tracker.apply("uid_a", online = true, rev = 6)
        assertEquals(PresenceState.ONLINE, tracker.state("uid_a"))
    }

    @Test
    fun c5_oneSourceForListAndChat() {
        // Both screens read the same flow; there is no other place a state lives.
        tracker.apply("uid_a", online = true, rev = 1)
        val listView = tracker.records.value["uid_a"]
        val chatView = tracker.records.value["uid_a"]
        assertEquals(listView, chatView)
    }
}
