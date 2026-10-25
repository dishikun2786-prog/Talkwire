// Publishing, delivery, history and the per-user publish rate limit.

package main

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

func (h *Hub) handlePub(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeAuth, "not authenticated"))
		return
	}
	// 消息发送频率兜底：handlePub 此前只校验长度，完全没有速率限制——一个失控
	// 或恶意的客户端可以按网络能承受的最快速度往任意会话灌消息。按 uid 而不是
	// 按 topic 限，否则同一个人开更多会话就能把有效速率乘上去；1:1 与群聊
	// 共用同一计数，两边都是需要防的场景。这是一道安全网，不是产品级节流——
	// 默认值（每10秒20条）远高于正常聊天的实际速度。
	if !tryAcquirePublish(client.uid, CurrentRuntimeConfig().PublishRateLimitPer10s, pubRateWindow) {
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeRateLimited, "发送太快，请稍后再试"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeBadReq, "topic required"))
		return
	}
	if msg.Topic == TopicMe || msg.Topic == TopicFnd {
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeBadReq, "cannot publish to me/fnd"))
		return
	}
	// 上限改为运行期可配（group_chat.message_max_len），不再是编译期常量：
	// 调这个值以前必须重新出包并重启 im-go。
	if utf8.RuneCountInString(msg.Content) > CurrentRuntimeConfig().MessageMaxLen {
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeBadReq, "message too long"))
		return
	}

	topic := msg.Topic
	var actualTopic string

	if isUserUID(topic) {
		var ok bool
		actualTopic, _, ok = h.resolveP2PTopic(client, msg.Id, ActionPub, topic)
		if !ok {
			return
		}
	} else {
		actualTopic = topic
	}

	// 非 uid 形态的 topic 每次都要校验。改前有三个各自独立的缺口：只在「尚未订阅」
	// 时查（订阅过一次就永久免检）、只覆盖 grp_ 前缀（canonical p2p_ 可直接往别人的
	// 1:1 会话注入消息）、以及 err == nil && !isMember 在查询出错时放行。
	if !isUserUID(topic) && !h.ensureTopicMember(client, msg, ActionPub, actualTopic) {
		return
	}
	// Encrypted groups must never accept a plain pub: the whole point of
	// e2e_set_group_encrypted is that from that moment on, every member's
	// client believes this topic's content is unreadable server-side, and a
	// plaintext pub landing in im_messages — client bug, stale/unencrypted
	// build, or a deliberately malicious client — would be a silent breach
	// of that belief for everyone else in the group. 1:1 encrypted threads
	// need no equivalent check here: they live under a wholly separate
	// e2e_ topic namespace that resolveP2PTopic never produces, so there is
	// no shared topic id for a plain pub to collide with in the first place.
	if strings.HasPrefix(actualTopic, "grp_") {
		if encrypted, err := h.store.IsGroupEncrypted(actualTopic); err == nil && encrypted {
			client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeConflict, "group_encrypted"))
			return
		}
	}
	if !client.IsSubscribed(actualTopic) {
		client.Subscribe(actualTopic)
	}

	var extra json.RawMessage
	if msg.Extra != nil && string(msg.Extra) != "null" {
		extra = msg.Extra
	}

	seq, tsMillis, duplicate, err := h.deliverMessage(actualTopic, client.uid, SenderKindUser, msg.Content, msg.ContentType, msg.FileURL, msg.FileName, extra, msg.Duration, nil, msg.ClientMsgId)
	if err != nil {
		logf("Save message failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeInternal, "save failed"))
		return
	}

	// TOCTOU close: the check at line ~612 and this write are not one atomic
	// operation, so a concurrent e2e_set_group_encrypted commit in that
	// narrow window would have let a plaintext message land in im_messages
	// for a group everyone now believes is encrypted. Re-checking a SECOND
	// time right after the write, and self-correcting via the existing
	// recall path if the race actually happened, turns "silently and
	// permanently wrong" into "corrected within milliseconds and the sender
	// is told to retry" — not a perfect atomic guarantee (that would need
	// deliverMessage itself, shared by forwards/AI replies/every plain send,
	// to take a per-topic advisory lock spanning both the check and the
	// write), but the actual exposure window shrinks from "indefinite" to
	// "this one request's own round trip," which for a group message that
	// literally just got flagged encrypted moments ago is effectively closed.
	if !duplicate && strings.HasPrefix(actualTopic, "grp_") {
		if encrypted, err := h.store.IsGroupEncrypted(actualTopic); err == nil && encrypted {
			if _, _, delErr := h.store.SoftDeleteMessage(actualTopic, seq, client.uid); delErr != nil {
				logf("handlePub: TOCTOU self-correction failed to recall seq=%d topic=%s: %v", seq, actualTopic, delErr)
			}
			client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeConflict, "group_encrypted"))
			return
		}
	}

	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionPub, Code: CodeOK,
		Topic: msg.Topic, Seq: seq,
		Timestamp: tsMillis, Extra: extra,
	})

	if duplicate {
		// H3: this pub carried a clientMsgId already saved earlier -- a
		// resend of one whose ack the client never saw, not a new message.
		// The sender already got a correct ack above; nothing else (no
		// re-broadcast, no re-triggered AI QA dispatch) should happen a
		// second time for content everyone already received once.
		return
	}

	// @小桃子 group AI QA detection. This is the ONLY place that ever calls
	// dispatchAiQaTask from message content — deliverMessage itself (used by
	// forwards and by the AI's own replies) never scans content for mentions.
	// That's a structural isolation, not a runtime senderKind check: an AI
	// reply, or a forwarded copy of one, physically cannot re-trigger this
	// because it never passes through handlePub at all (see plan §8.3/§9.2.3).
	if strings.HasPrefix(actualTopic, "grp_") && hasValidAiMention(msg.Content) {
		go h.dispatchAiQaTask(actualTopic, client.uid, msg.Content)
	}
}

// ForwardMeta carries forward provenance to attach to a newly delivered
// message; nil means the message being delivered is not a forward.
type ForwardMeta struct {
	FromMsgID  int
	FromTopic  string
	ChainDepth int
}

// deliverMessage is the single entry point shared by every path that writes
// a message into a topic and needs it to actually reach people: user
// publishes (handlePub above), broadcast-mode forwards
// (handleForwardBroadcast), and AI QA replies (dispatchAiQaTask). It assigns
// a seq, persists the row, broadcasts it to online subscribers, and kicks off
// (async) FCM delivery to offline members — the four steps handlePub used to
// inline entirely by itself, which meant every other writer either had to
// duplicate them or silently skip the offline-push step (see plan §9.2.1).
//
// clientMsgID (H3): non-empty only from handlePub. When set, the save goes
// through Store.SaveMessageFullIdempotent instead of SaveMessageFull -- a
// resend of a pub whose ack the client never saw (connection dropped
// mid-round-trip; see ImWebSocketClient.kt's PUBLISH_PENDING handling)
// returns the ORIGINAL seq/duplicate=true instead of creating a second
// message with a new seq. duplicate=true also skips the broadcast/FCM step
// below -- everyone who was going to see this message already did, the
// first time. Forward and AI-reply callers always pass "" and always get
// duplicate=false, i.e. this is a no-op change for them.
//
// handleForwardBundle is the one caller that cannot go through this
// function at all: its N inserts must share a single DB transaction, so it
// calls the *Tx store primitives directly and then reuses announceMessage
// (this function's broadcast+push half) once per row after COMMIT.
func (h *Hub) deliverMessage(topic, fromUID, senderKind, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int, forwardMeta *ForwardMeta, clientMsgID string) (seq int, tsMillis int64, duplicate bool, err error) {
	var forwardFromMsgID *int
	var forwardFromTopic *string
	chainDepth := 0
	if forwardMeta != nil {
		id := forwardMeta.FromMsgID
		forwardFromMsgID = &id
		topicCopy := forwardMeta.FromTopic
		forwardFromTopic = &topicCopy
		chainDepth = forwardMeta.ChainDepth
	}
	if clientMsgID != "" {
		var dup bool
		var savedAt time.Time
		seq, savedAt, dup, err = h.store.SaveMessageFullIdempotent(topic, clientMsgID, fromUID, senderKind, content, contentType, fileURL, fileName, extra, duration, forwardFromMsgID, forwardFromTopic, chainDepth)
		if err != nil {
			return 0, 0, false, err
		}
		tsMillis = savedAt.UnixMilli()
		if dup {
			// Already broadcast the first time this clientMsgID was saved --
			// re-announcing now would re-deliver to every online member and
			// re-trigger AI QA dispatch a second time for the same @mention.
			// The caller (handlePub) still gets a correct seq+ts to ack with:
			// savedAt is the ORIGINAL insert time, not now() -- using now()
			// here would hand the client a timestamp materially later than
			// what every other participant already sorted this message at.
			return seq, tsMillis, true, nil
		}
	} else {
		seq, err = h.store.SaveMessageFull(topic, fromUID, senderKind, content, contentType, fileURL, fileName, extra, duration, forwardFromMsgID, forwardFromTopic, chainDepth)
		if err != nil {
			return 0, 0, false, err
		}
		tsMillis = time.Now().UnixMilli()
	}
	h.announceMessage(topic, seq, fromUID, senderKind, content, contentType, fileURL, fileName, extra, duration, forwardFromMsgID, forwardFromTopic, clientMsgID)
	return seq, tsMillis, false, nil
}

// announceMessage broadcasts an already-persisted message to online
// subscribers and asynchronously pushes FCM to offline members. Split out of
// deliverMessage purely so handleForwardBundle can reuse it after its own
// transaction commits — see deliverMessage's doc comment.
func (h *Hub) announceMessage(topic string, seq int, fromUID, senderKind, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int, forwardFromMsgID *int, forwardFromTopic *string, clientMsgID string) {
	// The `topic` argument is the CANONICAL topic id and is what the fan-out
	// below must route on. It is not what the payload may carry for P2P.
	//
	// Android keys a 1:1 conversation by the PEER'S UID, and normalizes an
	// incoming data frame with:
	//     topicName = if (rawTopic == myUserId && from.isNotBlank()) from else rawTopic
	// (ImWebSocketClient.kt:880). Putting the canonical "p2p_<hash>" on the
	// wire makes that branch never fire, so the message files under a topicId
	// nothing in the app ever queries and simply never appears — no Android
	// code recognizes the p2p_ prefix at all. handleGet and handleDel both
	// still answer with the client's own wire topic, which is why history and
	// recall kept working while only the realtime push regressed.
	//
	// BroadcastToTopic excludes the sender, so for a P2P topic every recipient
	// is by definition the other party, and their conversation key is exactly
	// fromUID. Deriving it here rather than echoing whatever the sender wrote
	// also makes the payload correct for the forward paths, which address
	// their target by canonical topic id.
	payloadTopic := topic
	if strings.HasPrefix(topic, "p2p_") {
		payloadTopic = fromUID
	}
	dataMsg := &ServerMessage{
		Action: ActionData, Topic: payloadTopic, From: fromUID,
		Seq: seq, Content: content, ContentType: contentType,
		FileURL: fileURL, FileName: fileName,
		Duration: duration, Timestamp: time.Now().UnixMilli(), Extra: extra,
		SenderKind: senderKind,
	}
	if forwardFromMsgID != nil {
		dataMsg.ForwardFromMsgID = *forwardFromMsgID
	}
	if forwardFromTopic != nil {
		dataMsg.ForwardFromTopic = *forwardFromTopic
	}
	if clientMsgID != "" {
		dataMsg.ClientMsgId = clientMsgID
	}
	// F-31：群消息的 data 帧改前不带群名。端侧收到一条来自尚未出现在本地会话
	// 列表里的 grp_ 会话的推送时，手上只有 topic 串和 from，会话标题只能先显示
	// 成 topic 号，必须再单独拉一次群列表才知道这是哪个群。
	// ServerMessage 里没有独立的 groupName 字段（protocol.go 不归本文件改），
	// 复用既有的 Name —— 它在 data 帧里本来恒为空，语义上不冲突。
	if strings.HasPrefix(topic, "grp_") {
		dataMsg.Name = h.groupNameCached(topic)
	}
	h.BroadcastToTopic(topic, dataMsg, fromUID)
	go h.pushOfflineRecipients(topic, fromUID, senderKind, content, contentType)
}

func (h *Hub) handleNote(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Topic == "" || msg.What == "" {
		return
	}
	noteMsg := &ServerMessage{
		Action: ActionInfo, Topic: msg.Topic,
		From: client.uid, What: msg.What, Seq: msg.Seq,
	}
	var topic string
	if isUserUID(msg.Topic) {
		var ok bool
		topic, _, ok = h.resolveP2PTopic(client, msg.Id, ActionNote, msg.Topic)
		if !ok {
			return
		}
	} else {
		topic = msg.Topic
		// F-13：`note` 是输入中/已读这类高频信号（每次按键一条），原来每条都要
		// 打一次 DB 做成员校验。h.topics 里存在这条**连接本身**（按身份比对，不
		// 只是 uid 相同）就说明它此前已经通过 sub/get/pub 的授权闸门才被
		// Client.Subscribe 登记进索引 —— Subscribe 是 h.topics 唯一的写入点。
		// 拿它当已授权的快路径，不在索引里才回落到真正的 DB 校验。
		// 已知残留：被解除好友时 RemoveP2PMembers 只删 DB 行、不摘在线连接的
		// 订阅，所以对方在重连之前仍可能收到一条「正在输入」。只有输入态信号、
		// 且重连即自愈，用它换掉每次按键一条查询是划算的。
		if !h.isSubscribedInIndex(topic, client) && !h.ensureTopicMember(client, msg, ActionNote, topic) {
			return
		}
	}
	// Group read receipts: persist the sender's read cursor before relaying,
	// scoped to grp_ topics and gated by group_chat.read_receipt_enabled
	// (reused from the existing, previously Android-bootstrap-only flag --
	// see RuntimeConfig). P2P already has its own single-recipient READ
	// status client-side and is deliberately left alone. Only "read" notes
	// hit the DB here, not every "kp" keypress -- see the F-13 note above
	// this function on why "kp" stays on the DB-free fast path.
	if msg.What == NoteRead && strings.HasPrefix(topic, "grp_") && CurrentRuntimeConfig().ReadReceiptEnabled {
		if err := h.store.UpsertReadCursor(topic, client.uid, msg.Seq); err != nil {
			logf("UpsertReadCursor(%s,%s,%d): %v", topic, client.uid, msg.Seq, err)
		}
	}
	h.BroadcastToTopic(topic, noteMsg, client.uid)
}

func (h *Hub) handleGet(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGet, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGet, CodeBadReq, "topic required"))
		return
	}

	// 默认页大小与硬上限都从运行期配置来（group_chat.history_page_size，
	// 注册表把它约束在 20..200）。越界一律回落到默认值，而不是钳到上限——
	// 一个要 100000 条的客户端多半是算错了，给它 200 条同样不是它想要的。
	//
	// 这段判据此前是就地写在这里的，而 store.GetMessages/GetMessagesAfter 里
	// 另有一套各自硬编码的 `>100 就压成 50`，跑在这一层之后：101..200 在这里
	// 放行、到 store 被静默改成 50，运营把 history_page_size 调到 100 以上完全
	// 无效。现在唯一的归一化实现在 config.go 的 normalizeHistoryLimit，这里和
	// store 层都调它，不要再在任何一层写第二套上限。
	limit := normalizeHistoryLimit(msg.Limit)

	var topic string
	isP2P := isUserUID(msg.Topic)
	peerOnline := false
	if isP2P {
		var ok bool
		topic, peerOnline, ok = h.resolveP2PTopic(client, msg.Id, ActionGet, msg.Topic)
		if !ok {
			return
		}
	} else {
		topic = msg.Topic
		if !h.ensureTopicMember(client, msg, ActionGet, topic) {
			return
		}
	}

	var msgs []StoredMessage
	var err error
	if msg.AfterSeq > 0 {
		msgs, err = h.store.GetMessagesAfter(topic, msg.AfterSeq, limit)
	} else {
		msgs, err = h.store.GetMessages(topic, msg.BeforeSeq, limit)
	}
	if err != nil {
		logf("Get messages failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionGet, CodeInternal, "retrieve failed"))
		return
	}
	clearState, err := h.store.GetAdminTopic(topic)
	if err != nil {
		logf("Get topic clear state failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionGet, CodeInternal, "retrieve failed"))
		return
	}

	// Read-cursor baseline: checked by prefix, not isP2P, because a client
	// can also `get` a P2P topic by its already-resolved p2p_ topic string
	// (isUserUID(msg.Topic) is false in that case), which must not be
	// mistaken for a group. Omitted entirely (nil -> omitempty) unless the
	// feature is on, so a disabled deployment sends exactly the same
	// response shape as before this field existed.
	var readCursors []ReadCursorDto
	if strings.HasPrefix(topic, "grp_") && CurrentRuntimeConfig().ReadReceiptEnabled {
		cursors, cerr := h.store.GetReadCursors(topic)
		if cerr != nil {
			logf("GetReadCursors(%s): %v", topic, cerr)
		} else {
			readCursors = make([]ReadCursorDto, len(cursors))
			for i, c := range cursors {
				readCursors[i] = ReadCursorDto{UID: c.UID, Seq: c.Seq}
			}
		}
	}

	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionGet, Code: CodeOK, Topic: msg.Topic, Messages: msgs,
		ClearVersion: clearState.ClearVersion, ClearBeforeSeq: clearState.ClearBeforeSeq,
		ReadCursors: readCursors,
	})
	client.Subscribe(topic)
	// handleGet subscribes exactly like handleSub, but used to skip the
	// presence snapshot handleSub sends. Loading history is the path a client
	// actually takes once its process-lifetime knownTopics set has started
	// suppressing re-subscribes, so the topic got registered for delivery
	// while the peer's status was never reported at all.
	if isP2P {
		status := PresOffline
		if peerOnline {
			status = PresOnline
		}
		client.SendJSON(&ServerMessage{
			Action: ActionPres, Topic: msg.Topic, What: status,
			Online: peerOnline, PresRev: h.currentPresRev(msg.Topic),
		})
	}
}

// pubRateWindow is the fixed window tryAcquirePublish resets on. Not
// operator-configurable like the count is (CurrentRuntimeConfig().
// PublishRateLimitPer10s) -- the window shape is an implementation detail,
// the per-window budget is the operator-facing knob.
const pubRateWindow = 10 * time.Second

// pubRateEntry is one uid's fixed-window publish counter. Its own mutex
// (rather than relying on pubRateLimiter's map-level concurrency safety)
// guards the read-modify-write of windowStart/count against two publishes
// from the same uid landing on different goroutines at once -- sync.Map only
// makes the map itself safe to share, not the struct a Load returns.
type pubRateEntry struct {
	mu          sync.Mutex
	windowStart time.Time
	count       int
}

// pubRateLimiter tracks every uid that has ever published, for the lifetime
// of the process. Never purged -- same tradeoff groupNameCache and
// aiCooldownMap already make in this file, and at a few dozen bytes per
// entry it would take an enormous, indefinitely-growing user base before
// this is a real memory concern.
var pubRateLimiter sync.Map // uid -> *pubRateEntry

// tryAcquirePublish reports whether uid may publish one more message right
// now, under a fixed-window limit of maxPerWindow per window. Mirrors
// tryAcquireAiCooldown's LoadOrStore-then-CAS-free-because-per-entry-locked
// shape: the entry itself, once created, is never replaced, only mutated
// under its own lock, so there is no lost-update window between two
// goroutines racing to create the first entry for a brand-new uid either.
func tryAcquirePublish(uid string, maxPerWindow int, window time.Duration) bool {
	v, _ := pubRateLimiter.LoadOrStore(uid, &pubRateEntry{windowStart: time.Now()})
	e := v.(*pubRateEntry)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if now.Sub(e.windowStart) >= window {
		e.windowStart = now
		e.count = 0
	}
	if e.count >= maxPerWindow {
		return false
	}
	e.count++
	return true
}
