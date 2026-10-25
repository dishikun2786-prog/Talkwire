// Forwarding: merged bundles and one-to-many broadcast with an idempotency cache.

package main

import (
	"time"
)

// ---------------------------------------------------------------------------
// Batch forward (forward_bundle / forward_broadcast)
// ---------------------------------------------------------------------------

const (
	// forwardDepthLimit rejects re-forwarding a message that's already been
	// forwarded this many times, so a chain of forwards can't recurse
	// indefinitely (plan §2.2(1)/§9.3 medium-4).
	forwardDepthLimit = 3
)

// handleForwardBundle copies an ordered list of messages from one topic into
// a single target topic inside one DB transaction: either every message
// lands (in the caller's requested order) or none do. This is the "多条消息
// →一个群" mode — see plan §3.2 for why it needs all-or-nothing semantics
// where forward_broadcast (below) deliberately does not.
func (h *Hub) handleForwardBundle(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		// Must go through sendForwardBundleResult (carries msg.ReqID), not
		// client.SendJSON(ErrorResponse(msg.Id, ...)): ErrorResponse produces a plain
		// ServerMessage with no "reqId" field, which the Android client's
		// sendReqIdRequest() has no way to correlate back to its pending
		// CompletableDeferred — the caller would silently hang until its own
		// timeout instead of getting an immediate answer. Every other
		// early-return in this function already gets this right; this one
		// didn't (see plan §9.2.3 follow-up).
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "not_authenticated")
		return
	}
	if msg.SourceTopic == "" || msg.TargetTopic == "" || len(msg.SourceMsgIDs) == 0 {
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "bad_request")
		return
	}
	// 合并转发的条数上限改为运行期可配（group_chat.forward_bundle_max），
	// 不再是编译期常量。
	if len(msg.SourceMsgIDs) > CurrentRuntimeConfig().ForwardBundleMax {
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "too_many_messages")
		return
	}

	// Caller must be able to read the source topic and publish into the
	// target — both are group topics for this feature, so IsGroupMember
	// covers both checks (plan §5.1).
	if isMember, err := h.store.IsGroupMember(msg.SourceTopic, client.uid); err != nil || !isMember {
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "not_member")
		return
	}
	if isMember, err := h.store.IsGroupMember(msg.TargetTopic, client.uid); err != nil || !isMember {
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "not_member")
		return
	}

	// Fetch every source message up front, in the caller's requested order,
	// and reject the whole batch before touching the DB if any one is
	// missing or already too deep in the forward chain — "all or nothing" is
	// the entire point of bundle mode.
	originals := make([]*ForwardableMessage, 0, len(msg.SourceMsgIDs))
	for _, seq := range msg.SourceMsgIDs {
		m, err := h.store.GetMessageForForward(msg.SourceTopic, seq)
		if err != nil {
			h.sendForwardBundleResult(client, msg.ReqID, false, nil, "source_not_found")
			return
		}
		if m.ForwardChainDepth >= forwardDepthLimit {
			h.sendForwardBundleResult(client, msg.ReqID, false, nil, "forward_chain_too_deep")
			return
		}
		originals = append(originals, m)
	}

	tx, err := h.store.BeginTx()
	if err != nil {
		logf("handleForwardBundle: begin tx failed: %v", err)
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "internal_error")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	type pendingAnnounce struct {
		seq  int
		orig *ForwardableMessage
	}
	pending := make([]pendingAnnounce, 0, len(originals))
	insertedSeqs := make([]int, 0, len(originals))

	for _, orig := range originals {
		newSeq, err := h.store.GetNextSeqTx(tx, msg.TargetTopic)
		if err != nil {
			logf("handleForwardBundle: assign seq failed: %v", err)
			h.sendForwardBundleResult(client, msg.ReqID, false, nil, "internal_error")
			return
		}
		srcSeq := orig.Seq
		srcTopic := msg.SourceTopic
		if err := h.store.SaveMessageFullTx(tx, msg.TargetTopic, newSeq, client.uid, SenderKindUser,
			orig.Content, orig.ContentType, orig.FileURL, orig.FileName, orig.Extra, orig.Duration,
			&srcSeq, &srcTopic, orig.ForwardChainDepth+1); err != nil {
			logf("handleForwardBundle: insert failed: %v", err)
			h.sendForwardBundleResult(client, msg.ReqID, false, nil, "internal_error")
			return
		}
		insertedSeqs = append(insertedSeqs, newSeq)
		pending = append(pending, pendingAnnounce{seq: newSeq, orig: orig})
	}

	if err := tx.Commit(); err != nil {
		logf("handleForwardBundle: commit failed: %v", err)
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "internal_error")
		return
	}
	committed = true

	h.sendForwardBundleResult(client, msg.ReqID, true, insertedSeqs, "")

	// Broadcast/push only after COMMIT — announcing a row that later rolled
	// back would show recipients a message the DB never actually kept.
	for _, p := range pending {
		srcSeq := p.orig.Seq
		srcTopic := msg.SourceTopic
		h.announceMessage(msg.TargetTopic, p.seq, client.uid, SenderKindUser,
			p.orig.Content, p.orig.ContentType, p.orig.FileURL, p.orig.FileName, p.orig.Extra, p.orig.Duration,
			&srcSeq, &srcTopic, "")
	}
	logf("[Forward] %s bundled %d message(s) from %s into %s", client.uid, len(insertedSeqs), msg.SourceTopic, msg.TargetTopic)
}

func (h *Hub) sendForwardBundleResult(client *Client, reqID string, ok bool, insertedSeqs []int, reason string) {
	client.SendAny(&ForwardBundleResult{
		Type: TypeForwardBundleResult, ReqId: reqID, Ok: ok,
		InsertedSeqs: insertedSeqs, Reason: reason,
	})
}

// forwardReqCacheTTL is how long handleForwardBroadcast remembers a reqId's
// outcome, so a client that reconnects mid-flight and replays the same
// request gets back the original results instead of forwarding the message
// into every target group a second time (plan §8.1 — "本方案中唯一需要引入幂等
// 机制的地方").
const forwardReqCacheTTL = 5 * time.Minute

type cachedForwardResult struct {
	results   []ForwardTargetResult
	expiresAt time.Time
}

// inFlightForwardMarker occupies forwardReqCache for a reqID for the
// duration of one handleForwardBroadcast call. Without it, the cache was
// pure check-then-act: a concurrent replay of the same reqID (the exact
// scenario this cache exists to guard against — see forwardReqCacheTTL's
// doc comment) would Load a miss while the original call was still mid-loop
// (it only Stores its result at the very end), race past the check, and
// forward the message into every target topic a second time. Claiming the
// slot up front via LoadOrStore closes that window: a second caller for the
// same reqID sees the marker (not a miss) and is rejected instead of
// re-executing the forward.
var inFlightForwardMarker = &struct{}{}

type forwardBroadcastClaim int

const (
	// forwardClaimOwn means no entry existed for this reqID (or the only
	// entry present was a stale, already-expired result); the caller now
	// owns the slot and must call finishForwardBroadcast when done so the
	// marker doesn't block this reqID forever.
	forwardClaimOwn forwardBroadcastClaim = iota
	// forwardClaimCached means a completed, unexpired result was found;
	// use it verbatim, do not forward again.
	forwardClaimCached
	// forwardClaimInFlight means another goroutine is currently executing
	// this exact reqID right now; do not forward again.
	forwardClaimInFlight
)

// claimForwardBroadcast attempts to atomically claim reqID for execution.
// See forwardBroadcastClaim's cases for what the three outcomes mean and
// what the caller must do in each. Empty reqID always returns
// forwardClaimOwn (matching the pre-existing behavior of skipping dedup
// entirely when no reqID was supplied — see the empty-string guards this
// replaces in the old getCachedForwardBroadcastResult).
func (h *Hub) claimForwardBroadcast(reqID string) (forwardBroadcastClaim, []ForwardTargetResult) {
	if reqID == "" {
		return forwardClaimOwn, nil
	}
	for {
		actual, loaded := h.forwardReqCache.LoadOrStore(reqID, inFlightForwardMarker)
		if !loaded {
			return forwardClaimOwn, nil
		}
		if actual == inFlightForwardMarker {
			return forwardClaimInFlight, nil
		}
		entry := actual.(cachedForwardResult)
		if !time.Now().After(entry.expiresAt) {
			return forwardClaimCached, entry.results
		}
		// Completed-but-expired entry the periodic gc sweep hasn't cleaned
		// up yet (gcForwardReqCache runs every 1m while entries live 5m).
		// Reclaim the slot for a fresh attempt instead of replaying a stale
		// result, then loop and retry the LoadOrStore.
		h.forwardReqCache.CompareAndDelete(reqID, actual)
	}
}

// finishForwardBroadcast records reqID's outcome and — critically —
// overwrites whatever inFlightForwardMarker claimForwardBroadcast placed
// there, releasing the claim so a later legitimate reqID reuse (or a
// concurrent forwardClaimInFlight caller that already replied with a
// failure) isn't left permanently blocked against an in-flight marker that
// will never clear.
func (h *Hub) finishForwardBroadcast(reqID string, results []ForwardTargetResult) {
	if reqID == "" {
		return
	}
	h.forwardReqCache.Store(reqID, cachedForwardResult{results: results, expiresAt: time.Now().Add(forwardReqCacheTTL)})
}

// gcForwardReqCache periodically evicts expired forward_broadcast idempotency
// entries so a long-running process doesn't accumulate one map entry per
// forward request forever. Started once from main.go alongside hub.Run().
func (h *Hub) gcForwardReqCache() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		h.forwardReqCache.Range(func(k, v interface{}) bool {
			// In-flight markers aren't cachedForwardResult — leave them;
			// finishForwardBroadcast will replace the marker once that
			// request completes.
			entry, ok := v.(cachedForwardResult)
			if ok && now.After(entry.expiresAt) {
				h.forwardReqCache.Delete(k)
			}
			return true
		})
	}
}

// handleForwardBroadcast copies one message into each of several target
// topics independently: a failure in one target (already left the group,
// group disbanded, forward chain too deep) does not roll back the others.
// This is the "一条消息→多个群" mode — see plan §3.1 for why best-effort is the
// right semantics here, unlike forward_bundle's all-or-nothing.
func (h *Hub) handleForwardBroadcast(client *Client, msg *ClientMessage) {
	// fail is declared before the early validation checks below (not just
	// before the business-logic checks that originally used it) so that
	// *every* rejection path — including "not authenticated" and "missing
	// required fields" — replies through the typed, reqId-bearing
	// ForwardBroadcastResult. Replying via client.SendJSON(ErrorResponse(msg.Id, ...))
	// instead (as those two checks did before) produces a plain ServerMessage
	// with no "reqId" field, which the Android client's sendReqIdRequest()
	// cannot correlate back to its pending CompletableDeferred — the caller
	// would silently hang until its own timeout instead of getting an
	// immediate answer (see plan §9.2.3 follow-up).
	fail := func(reason string) {
		results := make([]ForwardTargetResult, 0, len(msg.TargetTopics))
		for _, t := range msg.TargetTopics {
			results = append(results, ForwardTargetResult{Topic: t, Ok: false, Reason: reason})
		}
		h.finishForwardBroadcast(msg.ReqID, results)
		client.SendAny(&ForwardBroadcastResult{Type: TypeForwardBroadcastResult, ReqId: msg.ReqID, Results: results})
	}

	if !client.IsAuthenticated() {
		fail("not_authenticated")
		return
	}
	if msg.SourceTopic == "" || msg.SourceMsgID <= 0 || len(msg.TargetTopics) == 0 {
		fail("bad_request")
		return
	}
	// 逐条转发的目标数改前**完全没有上限**：一次请求就能让服务端串行地往任意
	// 多个群各写一条消息、各自再触发一轮广播与离线推送。
	if len(msg.TargetTopics) > CurrentRuntimeConfig().ForwardBroadcastMax {
		fail("too_many_targets")
		return
	}

	switch claim, cached := h.claimForwardBroadcast(msg.ReqID); claim {
	case forwardClaimCached:
		client.SendAny(&ForwardBroadcastResult{Type: TypeForwardBroadcastResult, ReqId: msg.ReqID, Results: cached})
		return
	case forwardClaimInFlight:
		// Another goroutine — most likely this exact request replayed by a
		// client that reconnected mid-flight — is already forwarding this
		// message right now. Do not forward it a second time; fail this
		// attempt instead. A legitimate retry a moment later will either
		// hit forwardClaimCached (the original finished) or get a fresh
		// attempt once the entry expires.
		fail("duplicate_in_flight")
		return
	}
	// forwardClaimOwn: this call now owns reqID's slot in forwardReqCache
	// and must reach finishForwardBroadcast (directly or via fail()) on
	// every remaining exit path below, or the slot is left claimed forever.

	if isMember, err := h.store.IsGroupMember(msg.SourceTopic, client.uid); err != nil || !isMember {
		fail("not_member")
		return
	}
	original, err := h.store.GetMessageForForward(msg.SourceTopic, msg.SourceMsgID)
	if err != nil {
		fail("source_not_found")
		return
	}
	if original.ForwardChainDepth >= forwardDepthLimit {
		fail("forward_chain_too_deep")
		return
	}

	results := make([]ForwardTargetResult, 0, len(msg.TargetTopics))
	for _, targetTopic := range msg.TargetTopics {
		exists, err := h.store.TopicExists(targetTopic)
		if err != nil || !exists {
			results = append(results, ForwardTargetResult{Topic: targetTopic, Ok: false, Reason: "topic_not_found"})
			continue
		}
		isMember, err := h.store.IsGroupMember(targetTopic, client.uid)
		if err != nil || !isMember {
			results = append(results, ForwardTargetResult{Topic: targetTopic, Ok: false, Reason: "not_member"})
			continue
		}
		srcSeq := original.Seq
		srcTopic := msg.SourceTopic
		seq, _, _, err := h.deliverMessage(targetTopic, client.uid, SenderKindUser,
			original.Content, original.ContentType, original.FileURL, original.FileName,
			original.Extra, original.Duration,
			&ForwardMeta{FromMsgID: srcSeq, FromTopic: srcTopic, ChainDepth: original.ForwardChainDepth + 1}, "")
		if err != nil {
			logf("handleForwardBroadcast: deliver to %s failed: %v", targetTopic, err)
			results = append(results, ForwardTargetResult{Topic: targetTopic, Ok: false, Reason: "internal_error"})
			continue
		}
		results = append(results, ForwardTargetResult{Topic: targetTopic, Ok: true, Seq: seq})
	}

	h.finishForwardBroadcast(msg.ReqID, results)
	client.SendAny(&ForwardBroadcastResult{Type: TypeForwardBroadcastResult, ReqId: msg.ReqID, Results: results})
	logf("[Forward] %s broadcast msg %d/%s to %d target(s)", client.uid, msg.SourceMsgID, msg.SourceTopic, len(msg.TargetTopics))
}
