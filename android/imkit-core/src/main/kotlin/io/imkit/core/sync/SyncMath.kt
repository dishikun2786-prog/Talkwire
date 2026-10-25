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

    /** A larger gap than this between the bounds of a page is not filled in seq by seq. */
    public const val MAX_ACCOUNTED_GAP: Long = 10_000

    /**
     * The seqs a page proves gone (spec 8.2). The server returns every
     * remaining message with a seq in `(low, high]`: `low` is [afterSeq], or
     * just below the first seq of the newest page (older history is only not
     * loaded); `high` is the page's last seq. When nothing comes after the
     * page (it was not [full], or it is the newest page) `high` reaches
     * [lastSeq] too, and every local seq beyond it is gone as well. A seq in
     * there the page lacks was recalled: either one held here (it becomes a
     * tombstone) or one never received, which has to be accounted for all the
     * same, or the end of the local contiguous run would stay in front of it
     * and every later sync would start again from it.
     *
     * @param local the seqs held before the request was sent; a message pushed while it was in flight may be newer than the page
     * @param afterSeq the request's `afterSeq`; 0 for the newest page
     * @param page the seqs the page returned
     * @param full whether the page was full: more may come after it
     * @param lastSeq the conversation's `lastSeq` from `sync_summary` (spec 8.1), when known
     * @param floor the clear watermark: nothing at or below it is a gap
     */
    public fun seqsGone(
        local: Collection<Long>,
        afterSeq: Long,
        page: List<Long>,
        full: Boolean,
        lastSeq: Long? = null,
        floor: Long = 0,
    ): List<Long> {
        val returned = page.toHashSet()
        val newest = afterSeq <= 0
        val atEnd = newest || !full
        val low = maxOf(if (newest) (page.minOrNull()?.minus(1) ?: 0) else afterSeq, floor)
        var high = page.maxOrNull() ?: low
        if (atEnd && lastSeq != null) high = maxOf(high, lastSeq)
        val gone = java.util.TreeSet<Long>()
        if (high - low <= MAX_ACCOUNTED_GAP) {
            for (s in low + 1..high) if (s !in returned) gone += s
        } else {
            // Too wide to list one by one: account for what is held only.
            for (s in local) if (s > low && s <= high && s !in returned) gone += s
        }
        if (!newest && !full) for (s in local) if (s > high && s !in returned) gone += s
        return gone.filter { it > floor }
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
