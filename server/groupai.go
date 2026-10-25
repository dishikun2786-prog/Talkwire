// Group AI Q&A (optional module): settings cache, mention detection, cooldown and dispatch.

package main

import (
	"database/sql"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Group AI QA settings (get_group_ai_settings / update_group_ai_settings)
// ---------------------------------------------------------------------------

// groupAiSettingsCacheTTL bounds how stale a cached read can be after a
// group's settings change through some path other than
// handleUpdateGroupAiSettings (which invalidates the cache immediately on
// write). Matches plan §4.4's 60s figure.
const groupAiSettingsCacheTTL = 60 * time.Second

type groupAiSettingsCacheEntry struct {
	settings  *GroupAiSettings
	expiresAt time.Time
}

// GetGroupAiSettingsCached is the read-through cache in front of
// Store.GetGroupAiSettings, used by dispatchAiQaTask so a burst of @小桃子
// mentions in the same group doesn't each round-trip to Postgres.
func (h *Hub) GetGroupAiSettingsCached(topic string) (*GroupAiSettings, error) {
	if v, ok := h.groupAiSettingsCache.Load(topic); ok {
		entry := v.(groupAiSettingsCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.settings, nil
		}
	}
	settings, err := h.store.GetGroupAiSettings(topic)
	if err != nil {
		return nil, err
	}
	h.groupAiSettingsCache.Store(topic, groupAiSettingsCacheEntry{settings: settings, expiresAt: time.Now().Add(groupAiSettingsCacheTTL)})
	return settings, nil
}

func (h *Hub) invalidateGroupAiSettingsCache(topic string) {
	h.groupAiSettingsCache.Delete(topic)
}

// sendGroupAiSettingsFallback replies to get_group_ai_settings with the same
// disabled/no-KB shape GetGroupAiSettings returns for a group that has never
// configured AI QA, tagged with the caller's reqId. Used for this handler's
// three reject/error paths instead of client.SendJSON(ErrorResponse(msg.Id, ...)):
// GroupAiSettingsMessage's field set is a locked cross-team contract (plan
// §9 "跨端协议契约") that intentionally has no ok/reason field to carry a
// distinct failure signal, and ErrorResponse's plain ServerMessage carries no
// "reqId" at all — the Android client's sendReqIdRequest() would have no way
// to correlate it back to the pending CompletableDeferred, so the caller
// would silently hang for the full timeout instead of getting an answer.
// Falling back to "AI QA looks disabled" fails closed (never answers on the
// group's behalf) rather than open, which is the safer default for the two
// paths that should be unreachable from a well-formed client (not
// authenticated / empty topic) as well as for the one that's a genuine
// backend hiccup (DB error).
func (h *Hub) sendGroupAiSettingsFallback(client *Client, reqID, topic string) {
	client.SendAny(&GroupAiSettingsMessage{
		Type: TypeGroupAiSettings, ReqId: reqID, Topic: topic,
		AiQaEnabled: false, AiKbIds: []string{}, AiDocIds: []string{},
		AiDailyQuota: 50, AiCooldownSec: 5,
	})
}

func (h *Hub) handleGetGroupAiSettings(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		h.sendGroupAiSettingsFallback(client, msg.ReqID, msg.Topic)
		return
	}
	if msg.Topic == "" {
		h.sendGroupAiSettingsFallback(client, msg.ReqID, msg.Topic)
		return
	}
	// Prior to this fix there was no membership check at all: any authenticated
	// client supplying any group topic string -- including one for a group
	// they were kicked from or never joined -- could read that group's AI
	// Q&A config, including which knowledge base doc IDs it's bound to.
	// Checked inline rather than via ensureTopicMember (the shared helper
	// every other action uses) because this action replies over the
	// reqId-correlated channel (SendAny / sendGroupAiSettingsFallback), and
	// ensureTopicMember's ErrorResponse carries no reqId field -- reusing it
	// here would leave the Android client's sendReqIdRequest() waiting for a
	// response that can never be matched, stuck until its own timeout
	// instead of failing fast. Same reasoning as the comment on
	// handleUpdateGroupAiSettings's auth-failure branch just below.
	// Fail-closed on a DB error, same as ensureTopicMember does.
	if isMember, err := h.store.IsGroupMember(msg.Topic, client.uid); err != nil || !isMember {
		h.sendGroupAiSettingsFallback(client, msg.ReqID, msg.Topic)
		return
	}
	settings, err := h.GetGroupAiSettingsCached(msg.Topic)
	if err != nil {
		h.sendGroupAiSettingsFallback(client, msg.ReqID, msg.Topic)
		return
	}
	client.SendAny(&GroupAiSettingsMessage{
		Type: TypeGroupAiSettings, ReqId: msg.ReqID, Topic: msg.Topic,
		AiQaEnabled: settings.AiQaEnabled, AiKbIds: settings.AiKbIds, AiDocIds: settings.AiDocIds,
		AiDailyQuota: settings.AiDailyQuota, AiCooldownSec: settings.AiCooldownSec,
	})
}

// handleUpdateGroupAiSettings is owner-only (plan §5.1), gated by IsGroupOwner
// exactly as handleDisbandGrp is.
//
// This used to name handleKick as the second precedent. That is now wrong and
// misleading in the worst direction: handleKick's primary gate is
// ensureGroupManager (owner OR admin), and IsGroupOwner survives there only in
// the narrow sub-branch that stops one admin from removing another. Reading
// handleKick as an owner-only precedent would suggest kicking is still owner-
// exclusive, which is the opposite of what it does.
func (h *Hub) handleUpdateGroupAiSettings(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		// Reply through the typed, reqId-bearing UpdateGroupAiSettingsResult
		// (same struct every other path below already uses), not
		// client.SendJSON(ErrorResponse(msg.Id, ...)): that produces a plain
		// ServerMessage with no "reqId" field, which the Android client's
		// sendReqIdRequest() cannot correlate back to its pending
		// CompletableDeferred — the caller would silently hang until its own
		// timeout instead of getting an immediate answer.
		client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: false, Reason: "not_authenticated"})
		return
	}
	if msg.Topic == "" {
		client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: false, Reason: "bad_request"})
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: false, Reason: "topic_not_found"})
		return
	}
	if !isOwner {
		client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: false, Reason: "not_owner"})
		return
	}
	kbIds := msg.AiKbIds
	if kbIds == nil {
		kbIds = []string{}
	}
	docIds := msg.AiDocIds
	if docIds == nil {
		docIds = []string{}
	}
	if err := h.store.UpsertGroupAiSettings(msg.Topic, msg.AiQaEnabled, kbIds, docIds, client.uid); err != nil {
		logf("handleUpdateGroupAiSettings: %v", err)
		client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: false, Reason: "internal_error"})
		return
	}
	// Invalidate immediately rather than waiting out the TTL — otherwise a
	// group owner who just disabled AI QA could still see it answer for up
	// to 60s (plan §9.3 low-7).
	h.invalidateGroupAiSettingsCache(msg.Topic)
	client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: true})
	logf("[GroupAiQA] %s updated settings for %s: enabled=%v kbIds=%d docIds=%d", client.uid, msg.Topic, msg.AiQaEnabled, len(kbIds), len(docIds))
}

// ---------------------------------------------------------------------------
// @小桃子 mention detection + AI QA dispatch
// ---------------------------------------------------------------------------

// aiMentionRegex matches "@小桃子" followed by ASCII/CJK whitespace, common
// CJK/ASCII punctuation, another "@" (multi-mention with no separator), or
// end of string — not just \s, which would miss the two most common real
// inputs: "@小桃子，帮我查一下"（全角逗号续写）and "@小桃子@张三"（紧邻多重艾特）.
// It still correctly rejects "@小桃子的忠实粉丝群" (not in the boundary set).
// See plan §9.2.3.
var aiMentionRegex = regexp.MustCompile(`@小桃子(?:[\s\x{3000}，。！？：；、,.!?:;@]|$)`)

func hasValidAiMention(content string) bool {
	return aiMentionRegex.MatchString(content)
}

// aiCooldownMap tracks, per group topic, the last time an AI QA task was
// dispatched. tryAcquireAiCooldown uses LoadOrStore/CompareAndSwap instead of
// a Load-then-Store pair so two @小桃子 messages arriving in the same group
// within the same instant can't both observe "not in cooldown" and both fire
// (plan §9.2.2 B3).
var aiCooldownMap sync.Map // topic -> time.Time

func tryAcquireAiCooldown(topic string, cooldownSec int) bool {
	now := time.Now()
	for {
		prevVal, loaded := aiCooldownMap.Load(topic)
		if !loaded {
			if _, actuallyLoaded := aiCooldownMap.LoadOrStore(topic, now); !actuallyLoaded {
				return true // we placed the first marker for this topic
			}
			continue // lost the race to another goroutine; re-read and retry
		}
		prev := prevVal.(time.Time)
		if now.Sub(prev) < time.Duration(cooldownSec)*time.Second {
			return false // still within cooldown
		}
		if aiCooldownMap.CompareAndSwap(topic, prevVal, now) {
			return true // atomically claimed this dispatch
		}
		// CAS lost to a concurrent goroutine; retry against its fresher value.
	}
}

// dispatchAiQaTask handles one @小桃子 mention end to end: settings lookup,
// per-group cooldown, a synchronous HTTP call to roubao's internal endpoint
// (which does its own per-group-owner credit/quota/knowledge-scope checks —
// im-go deliberately does not duplicate the daily-quota check here, see plan
// §9.2.2 B2), and finally delivering either the AI's answer or a friendly
// failure notice back into the group. Always invoked via `go` (see
// handlePub) so a slow/timed-out LLM call never blocks message broadcast for
// the rest of the group.
func (h *Hub) dispatchAiQaTask(topic, askerUID, question string) {
	// This whole function runs detached via `go h.dispatchAiQaTask(...)`
	// (see handlePub) with no caller left to observe a panic. Go's default
	// behavior for an unrecovered panic in any goroutine — including one
	// started with a bare `go` — is to crash the entire process, not just
	// this goroutine. Unlike the rest of this file's request-handling code,
	// this path is now reachable by any group member simply sending a
	// message containing "@小桃子", and it does real HTTP/DB work
	// (GetGroupAiSettingsCached, AskGroupAiQa, deliverMessage) plus type
	// assertions inside those calls — any one of those failing in an
	// unexpected way must not take down every other user's connection.
	defer func() {
		if r := recover(); r != nil {
			logf("dispatchAiQaTask: recovered panic for topic=%s: %v", topic, r)
		}
	}()
	settings, err := h.GetGroupAiSettingsCached(topic)
	if err != nil || settings == nil || !settings.AiQaEnabled {
		return
	}
	if !tryAcquireAiCooldown(topic, settings.AiCooldownSec) {
		// No WS feedback is defined by the protocol contract for a
		// cooldown-suppressed mention; silently dropping matches the current
		// contract. A "你问得有点快" style notice is a documented P2 follow-up
		// (plan §7 P2 acceptance criteria), not part of this contract.
		return
	}

	h.BroadcastAny(topic, &AiTypingMessage{Type: TypeAiTyping, Topic: topic}, "")

	groupInfo, err := h.store.GetGroupInfo(topic)
	if err != nil || groupInfo == nil || groupInfo.Owner == "" {
		h.deliverAiFallback(topic, aiQaFailureMessage(""))
		return
	}
	// Always resolve the owner fresh from im-go's own store rather than
	// trusting any caller-supplied value — im-go is the authority on "who
	// owns this group right now" (plan §8.2 table, last row).
	ownerAppUserID, err := h.resolveAppUserID(groupInfo.Owner)
	if err != nil {
		h.deliverAiFallback(topic, aiQaFailureMessage(""))
		return
	}

	answer, ok, reason := h.aiHost.AskGroupAiQa(topic, askerUID, question, ownerAppUserID, settings.AiKbIds, settings.AiDocIds)
	if !ok {
		h.deliverAiFallback(topic, aiQaFailureMessage(reason))
		return
	}
	if _, _, _, err := h.deliverMessage(topic, AiAssistantUID, SenderKindAiAssistant, answer, MsgTypeText, "", "", nil, 0, nil, "", nil); err != nil {
		logf("dispatchAiQaTask: deliver AI reply failed: %v", err)
	}
}

func (h *Hub) deliverAiFallback(topic, text string) {
	if _, _, _, err := h.deliverMessage(topic, AiAssistantUID, SenderKindAiAssistant, text, MsgTypeText, "", "", nil, 0, nil, "", nil); err != nil {
		logf("dispatchAiQaTask: deliver fallback failed: %v", err)
	}
}

// aiQaFailureMessage turns roubao's internal failure `reason` (per the
// protocol contract's enum) into a friendly group-chat message. Unknown/empty
// reasons — including transport failures, which AskGroupAiQa itself already
// normalizes to "timeout" — fall back to the same "还在整理资料" wording used for
// an actual timeout, since from the asker's point of view both look
// identical (no answer showed up in time).
func aiQaFailureMessage(reason string) string {
	switch reason {
	case "insufficient_credits":
		return "群主积分不足，小桃子暂时无法为本群解答问题"
	case "quota_exceeded":
		return "本群今日的小桃子问答次数已用完，请明天再试"
	case "not_configured":
		return "群主还没有为本群配置可用的知识库，请联系群主在群设置中勾选"
	case "owner_inactive":
		return "小桃子暂时无法为本群解答问题"
	case "rate_limited":
		return "问得有点快，请稍后再试"
	default:
		return "小桃子正在整理资料，请稍后再问"
	}
}

// groupAiModule answers questions asked in a group by mentioning the
// assistant. It reacts to new messages through the post-ack hook, the same
// way any bot would.
type groupAiModule struct{}

func (groupAiModule) name() string { return "groupai" }

func (groupAiModule) actions(h *Hub) map[string]func(*Client, *ClientMessage) {
	return map[string]func(*Client, *ClientMessage){
		ActionGetGroupAiSettings:    h.handleGetGroupAiSettings,
		ActionUpdateGroupAiSettings: h.handleUpdateGroupAiSettings,
	}
}

func (groupAiModule) afterAck(h *Hub, c *Client, msg *ClientMessage, topic string) {
	// @小桃子 group AI QA detection. This is the ONLY place that ever calls
	// dispatchAiQaTask from message content — deliverMessage itself (used by
	// forwards and by the AI's own replies) never scans content for mentions.
	// That's a structural isolation, not a runtime senderKind check: an AI
	// reply, or a forwarded copy of one, physically cannot re-trigger this
	// because it never passes through handlePub (and so this hook) at all (see plan §8.3/§9.2.3).
	if strings.HasPrefix(topic, "grp_") && hasValidAiMention(msg.Content) {
		go h.dispatchAiQaTask(topic, c.uid, msg.Content)
	}
}

func (groupAiModule) disbandTx(*sql.Tx, string) error { return nil }

// disbanded drops the settings cached for the group. F-21: the TTL is 60s,
// and a stale entry would carry the dead group's knowledge-base binding.
func (groupAiModule) disbanded(h *Hub, topic string) { h.invalidateGroupAiSettingsCache(topic) }
