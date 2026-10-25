// Recall (del) and clearing a conversation (del_topic), plus media garbage collection.

package main

import (
	"fmt"
	"strings"
)

func (h *Hub) handleDel(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionDel, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.Seq <= 0 {
		client.SendJSON(ErrorResponse(msg.Id, ActionDel, CodeBadReq, "topic and seq required"))
		return
	}
	// Resolve a P2P peer-UID topic to the canonical stored topic, mirroring
	// handlePub — messages live under p2p_<hash>, not the peer's raw UID, so
	// the old code (which deleted by the raw peer UID) silently matched nothing.
	actualTopic := msg.Topic
	if isUserUID(msg.Topic) {
		var ok bool
		actualTopic, _, ok = h.resolveP2PTopic(client, msg.Id, ActionDel, msg.Topic)
		if !ok {
			return
		}
	} else if !h.ensureTopicMember(client, msg, ActionDel, actualTopic) {
		// The canonical-topic (grp_/p2p_) branch had no membership check at
		// all -- only the uid_ branch above got one, via resolveP2PTopic.
		// The recall primitive's from_uid=caller scoping means the practical
		// damage was capped at "can only recall your own messages", but that still
		// let a non-member (or a former member kicked from the group) probe
		// arbitrary topic strings and, worse, recall a message they'd sent
		// before being removed -- retroactively altering history other
		// members still see. Same pattern handleDelTopic already uses for
		// this same class of gap.
		return
	}
	// F-15：改为软删（置 recalled_at，行保留）。硬删会把 (topic, seq) 这个位置
	// 从序列里抠掉，而端侧的离线补拉是「给我 seq > 本地最大值的消息」，序列一旦
	// 出现空洞就再也无法判断「这里本来有一条被撤回了」还是「这条我漏收了」。
	// 保留行让 seq 始终稠密，读取侧靠 recalled_at IS NULL 过滤。
	//
	// Own-messages-only recall: scoped to from_uid = caller, so a client can
	// never recall the other party's messages.
	mediaURLs, deleted, err := h.store.SoftDeleteMessage(actualTopic, msg.Seq, client.uid)
	if err != nil {
		logf("Delete failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionDel, CodeInternal, "delete failed"))
		return
	}
	if !deleted {
		// F-34：改前无论有没有真的撤回都先回 200 再判断。客户端据此把本地那条
		// 消息也删掉了，于是「撤回一条不属于自己的 seq」「撤回一条已经不存在的
		// seq」都会造成本地少一条、服务端还留着的静默不一致。
		client.SendJSON(ErrorResponse(msg.Id, ActionDel, CodeNotFound, "message not found or not yours"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionDel, Code: CodeOK, Topic: msg.Topic, Seq: msg.Seq})
	// Push the recall to the other online participant(s) so their copy vanishes
	// with no trace (distinct "recalled" action, addressed exactly like a data
	// push so the client's P2P-topic normalization files it correctly).
	//
	// Offline peers self-heal, but not for the reason this comment used to
	// give ("the row is already gone"): since the switch to soft delete the row
	// very much survives, as a tombstone with recalled_at set and its content
	// fields stripped. What omits it from their next fetch is the
	// `recalled_at IS NULL` predicate on every read path (GetMessages /
	// GetMessagesAfter). The row staying put is the point -- it keeps the
	// (topic, seq) series dense so gap detection does not chase it forever.
	h.BroadcastToTopic(actualTopic, &ServerMessage{Action: ActionRecalled, Topic: msg.Topic, Seq: msg.Seq, From: client.uid}, client.uid)
	// gcMediaBlob 自己会先 CountFileRefs，被别处（转发副本）引用就不删。软删
	// 的墓碑行虽然留在表里，但 file_url 与 extra 已被置 NULL，所以不会把自己的
	// blob 钉住。视频的首帧缩略图（extra->>'thumbUrl'）一并回收（计划 1.9）。
	for _, u := range mediaURLs {
		go h.gcMediaBlob(u)
	}
}

// handleDelTopic clears an entire conversation for everyone: every message in
// the topic is hard-deleted and its orphaned media blobs removed, then the
// other participant is told to clear their local copy too.
func (h *Hub) handleDelTopic(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionDelTopic, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionDelTopic, CodeBadReq, "topic required"))
		return
	}
	actualTopic := msg.Topic
	if isUserUID(msg.Topic) {
		var ok bool
		actualTopic, _, ok = h.resolveP2PTopic(client, msg.Id, ActionDelTopic, msg.Topic)
		if !ok {
			return
		}
	} else if strings.HasPrefix(actualTopic, "grp_") {
		// F-01/D1：群聊的「清空聊天记录」是给**所有人**硬删全部消息并回收媒体，
		// 不可逆。改前只要求「是群成员」，等于群里任何一个人都能一键抹掉整群的
		// 历史。收敛为群主或管理员。
		// 1:1 会话不走这条分支：那里双方本来就都是「自己会话的主人」，清空是
		// 双方共有的权利。但此前这里写的「仍由下面的 ensureTopicMember 兜住」
		// 只对三分之一的情形成立，实际是三条分支各管一段：
		//   - uid_/usr 形态：在上面第一个分支就被 resolveP2PTopic 消费掉了，
		//     授权来自它内部的 IsGroupMember/AreFriends，根本走不到下面那行；
		//   - grp_ 前缀：本分支，收敛为群主或管理员；
		//   - 裸 p2p_ 串：只有这一种才真的落到下面的 ensureTopicMember。
		if !h.ensureGroupManager(client, msg, ActionDelTopic, actualTopic) {
			return
		}
	} else if !h.ensureTopicMember(client, msg, ActionDelTopic, actualTopic) {
		// 改前这个分支只覆盖 grp_ 前缀，裸 p2p_ 串完全没有授权分支——而这条路径是
		// 不可逆的第三方数据销毁（硬删 im_messages + 回收媒体）。
		return
	}
	result, err := h.store.ClearTopicMessages(actualTopic)
	if err != nil {
		logf("Delete topic failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionDelTopic, CodeInternal, "clear failed"))
		return
	}
	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionDelTopic, Code: CodeOK, Topic: msg.Topic,
		ClearVersion: result.ClearVersion, ClearBeforeSeq: result.ClearBeforeSeq,
		DeletedMessages: result.DeletedMessages, DeletedMedia: result.DeletedMedia,
	})
	members, memberErr := h.store.GetTopicMemberUIDs(actualTopic)
	if memberErr != nil {
		logf("Clear topic member lookup failed: %v", memberErr)
	} else {
		h.broadcastTopicCleared(actualTopic, members, client.uid, result)
	}
	for _, u := range result.FileURLs {
		go h.gcMediaBlob(u)
	}
}

// ClearDirectTopicForAdmin is the privileged counterpart to handleDelTopic.
// It is deliberately exported only within the im-go process and called from a
// shared-secret HTTP handler; the main API must never delete im_messages
// directly because online clients need the same topic_cleared event.
func (h *Hub) ClearDirectTopicForAdmin(topic string) (ClearTopicResult, error) {
	info, err := h.store.GetAdminTopic(topic)
	if err != nil {
		return ClearTopicResult{}, err
	}
	if info.IsGroup || len(info.Members) != 2 {
		return ClearTopicResult{}, fmt.Errorf("only two-person direct conversations can be cleared by admin")
	}
	result, err := h.store.ClearTopicMessages(topic)
	if err != nil {
		return ClearTopicResult{}, err
	}
	// Admin-initiated deletion reaches both participants, including users who
	// are currently on the conversation screen. Offline clients reconcile the
	// clear version during their next history pull.
	h.broadcastTopicCleared(topic, info.Members, "", result)
	for _, u := range result.FileURLs {
		go h.gcMediaBlob(u)
	}
	return result, nil
}

// broadcastTopicCleared sends directly to active members instead of relying
// on topic subscriptions: a connected client may not currently have this
// older conversation subscribed, but its Room cache must still be cleared.
// P2P UI keys use the peer UID while database topics use p2p_<hash>, so each
// target receives its own peer UID as the wire topic.
//
// Members who are offline at this moment get a durable per-member tombstone
// instead (im_pending_clears) -- delivered and acked the next time they log
// in (see handleLogin -> deliverPendingClears / handleAckTopicCleared).
func (h *Hub) broadcastTopicCleared(topic string, members []string, excludeUID string, result ClearTopicResult) {
	// Group topics are always "grp_"-prefixed (CreateGroup); P2P topics are
	// "p2p_"-prefixed (GetP2PTopic) -- a structural fact about topic naming,
	// not a guess from headcount. The old len(members)==2 check would
	// misclassify a 2-person GROUP as P2P.
	isP2P := !strings.HasPrefix(topic, "grp_")
	for _, uid := range members {
		if uid == excludeUID {
			continue
		}
		wireTopic := topic
		if isP2P {
			for _, peerUID := range members {
				if peerUID != uid {
					wireTopic = peerUID
					break
				}
			}
		}
		target := h.GetClientByUID(uid)
		if target == nil {
			if err := h.store.EnqueuePendingClear(topic, uid, wireTopic, result.ClearVersion, result.ClearBeforeSeq, excludeUID); err != nil {
				logf("broadcastTopicCleared: enqueue pending clear failed for topic=%s uid=%s: %v", topic, uid, err)
			}
			continue
		}
		target.SendJSON(&ServerMessage{
			Action: ActionTopicCleared, Topic: wireTopic, From: excludeUID,
			ClearVersion: result.ClearVersion, ClearBeforeSeq: result.ClearBeforeSeq,
		})
	}
}

// gcMediaBlob asks the main server to delete a media blob, but only once no
// remaining message references it (media can be shared across chats via
// forwarding, which reuses the same file URL).
func (h *Hub) gcMediaBlob(fileURL string) {
	if h.host == nil || fileURL == "" {
		return
	}
	n, err := h.store.CountFileRefs(fileURL)
	if err != nil {
		logf("gcMediaBlob CountFileRefs failed: %v", err)
		return
	}
	if n > 0 {
		return // still referenced by another message
	}
	if err := h.host.DeleteMedia([]string{fileURL}); err != nil {
		logf("gcMediaBlob DeleteMedia failed: %v", err)
	}
}
