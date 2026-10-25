package io.imkit.core.sync

import io.imkit.core.ImIds
import io.imkit.core.model.ConversationId

/** Pure parts of the sync algorithm (protocol spec 8), kept apart so they can be tested alone. */
public object SyncMath {
    /**
     * Where an incremental pull starts: the end of the local **contiguous** run
     * of seqs, not the largest seq (C3). With `afterSeq = max` a hole below
     * the maximum — a dropped push, a missed subscription — would never be
     * asked for again; from the end of the run it costs one page of messages
     * already held.
     *
     * A recalled seq is accounted for (its row was deleted on purpose) and so
     * is everything at or below the clear watermark; neither is a hole.
     * Returns 0 when nothing is held: then only the newest page is fetched.
     */
    public fun resumeSeq(stored: Collection<Long>, recalled: Collection<Long>, clearBeforeSeq: Long): Long {
        val held = (stored.asSequence() + recalled.asSequence()).filter { it > clearBeforeSeq }.toSortedSet()
        // After a clear the run starts at the watermark; otherwise at the
        // oldest seq held (older history may simply never have been loaded).
        var end = when {
            clearBeforeSeq > 0 -> clearBeforeSeq
            held.isEmpty() -> return 0
            else -> held.first()
        }
        for (s in held) {
            if (s <= end) continue
            if (s == end + 1) end = s else break
        }
        return end
    }

    /**
     * Local seqs a page proves gone: the server returns every remaining
     * message with seq in `(afterSeq, last returned]`, so a local seq in that
     * range that the page lacks was recalled while we were away (spec 9.5).
     * Only meaningful for an `afterSeq` page that returned something.
     */
    public fun recalledWhileAway(local: Collection<Long>, afterSeq: Long, page: List<Long>): List<Long> {
        if (afterSeq <= 0 || page.isEmpty()) return emptyList()
        val last = page.max()
        val returned = page.toHashSet()
        return local.filter { it > afterSeq && it <= last && it !in returned }.sorted()
    }
}

/**
 * Files a pushed frame under a conversation (spec 5.5): groups by their topic,
 * direct chats by the peer's uid, whichever of the three spellings the frame
 * uses.
 */
public object TopicResolver {
    /**
     * @param topic the frame's `topic`
     * @param from the frame's `from` (may be empty)
     * @param me my uid
     * @param knownPeers uids to try when only a `p2p_…` topic is given
     */
    public fun conversationOf(topic: String, from: String, me: String, knownPeers: () -> Collection<String>): ConversationId? {
        if (topic.isEmpty()) return null
        if (ImIds.isGroupTopic(topic)) return ConversationId(topic)
        if (ImIds.isP2pTopic(topic)) {
            if (from.isNotEmpty() && from != me && ImIds.isUserUid(from)) return ConversationId(from)
            return knownPeers().firstOrNull { ImIds.p2pTopic(me, it) == topic }?.let { ConversationId(it) }
        }
        if (!ImIds.isUserUid(topic)) return null
        if (topic == me) {
            // The peer wrote my uid as the topic; the conversation is with the sender.
            return if (from.isNotEmpty() && from != me && ImIds.isUserUid(from)) ConversationId(from) else null
        }
        // Another uid: that is the peer, also when I am the sender on another device.
        return ConversationId(topic)
    }
}
