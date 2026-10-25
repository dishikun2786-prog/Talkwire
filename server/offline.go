// Offline queues replayed at login: conversation clears and group events, and their acks.

package main

import (
	"strconv"
)

// deliverPendingClears replays every clear this uid missed while offline,
// exactly mirroring the shape broadcastTopicCleared would have sent had they
// been online. Runs on a `go` goroutine off handleLogin so a slow/failing
// query never delays the login ack itself.
func (h *Hub) deliverPendingClears(client *Client) {
	pending, err := h.store.GetPendingClears(client.uid, client.Device())
	if err != nil {
		logf("deliverPendingClears: query failed for %s: %v", client.uid, err)
		return
	}
	for _, p := range pending {
		client.SendJSON(&ServerMessage{
			Id: strconv.FormatInt(p.ID, 10), Action: ActionTopicCleared,
			Topic: p.WireTopic, From: p.FromUID,
			ClearVersion: p.ClearVersion, ClearBeforeSeq: p.ClearBeforeSeq,
		})
	}
}

// handleAckTopicCleared is fire-and-forget: the client has already applied
// the clear locally by the time it sends this, so whether or not the delete
// succeeds only affects when the row is finally reclaimed, never correctness.
func (h *Hub) handleAckTopicCleared(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Id == "" {
		return
	}
	id, err := strconv.ParseInt(msg.Id, 10, 64)
	if err != nil {
		return
	}
	if err := h.store.AckPendingClear(id, client.uid, client.Device()); err != nil {
		logf("handleAckTopicCleared: %v", err)
	}
}

// actionAckGroupEvent 是客户端对一条补投递群事件的回执。
//
// 用非导出常量而不是加进 protocol.go 的 Action* 表：那个文件由另一路负责，
// 本文件不能改它，而这里必须有一个可用的判据。命名刻意小写以免与将来可能
// 加进 protocol.go 的 ActionAckGroupEvent 撞名。
const actionAckGroupEvent = "ack_group_event"

// pendingGroupEventPush 是从离线队列补投递出去的一条群事件。
//
// 帧形与在线时收到的 grp_sync / grp_disband 逐字一致（action/topic/name/from），
// 只多一个 eventId —— 老客户端会直接忽略这个多出来的键，新客户端拿它回
// ack_group_event 把队列行销掉。不复用 ServerMessage 是因为它没有 eventId
// 字段，而 protocol.go 不归本文件改；也刻意不挪用 ServerMessage.Id：那个字段
// 是请求-响应关联用的，补投递帧不是任何请求的响应，占用它会让客户端把这条
// 推送错配到某个在飞请求上（ImWebSocketClient 找不到精确 id 时会按 action
// 前缀兜底挑一个在飞请求，正是本仓库踩过的坑）。
type pendingGroupEventPush struct {
	Action  string `json:"action"`
	Topic   string `json:"topic"`
	Name    string `json:"name,omitempty"`
	From    string `json:"from,omitempty"`
	EventId int64  `json:"eventId"`
	// JoinStatus rides along only on a replayed group_join_reviewed push
	// (PendingGroupEventJoinApproved/Rejected) -- empty and omitted for the
	// pre-existing grp_sync/grp_disband kinds.
	JoinStatus string `json:"joinStatus,omitempty"`
}

// Group events for devices that were away are queued by deliverGroupEvent
// and queueMissing (devices.go): a membership change (joining, being
// kicked, a group disbanded) is a one-off event on the server, and once the
// member rows changed no later traffic corrects the local state of a device
// that was not connected.

// deliverPendingGroupEvents 重放这个 uid 离线期间错过的群事件。与
// deliverPendingClears 并列，在 handleLogin 末尾以 `go` 启动，慢查询不拖累
// 登录 ack 本身。
func (h *Hub) deliverPendingGroupEvents(client *Client) {
	pending, err := h.store.GetPendingGroupEvents(client.uid, client.Device())
	if err != nil {
		logf("deliverPendingGroupEvents: query failed for %s: %v", client.uid, err)
		return
	}
	for _, ev := range pending {
		switch ev.Kind {
		case ActionGrpSync:
			// 从入队到这个人上线之间，他可能已经被踢、或者群已经解散，这条
			// 同步就过期了。此时既不该推（端侧会凭空多出一个进不去的群），也
			// 不该留（永远重放）—— 直接销掉。
			isMember, memErr := h.store.IsGroupMember(ev.Topic, client.uid)
			if memErr != nil {
				// 查不动就原样留着下次登录再试，绝不当成「已不是成员」误删。
				logf("deliverPendingGroupEvents: membership check failed topic=%s uid=%s: %v", ev.Topic, client.uid, memErr)
				continue
			}
			if !isMember {
				if ackErr := h.store.DropPendingGroupEvent(ev.ID, client.uid); ackErr != nil {
					logf("deliverPendingGroupEvents: drop stale event %d: %v", ev.ID, ackErr)
				}
				continue
			}
			// 与在线邀请路径同一个理由：端侧的 handleGroupSync 只把 topic 记进
			// knownTopics 而**不订阅**，服务端不主动把它放进 h.topics 的话，
			// 这个群的实时消息他一条都收不到（离线推送又会因为他此刻在线而跳过）。
			client.Subscribe(ev.Topic)
			client.SendAny(&pendingGroupEventPush{
				Action: ActionGrpSync, Topic: ev.Topic, Name: ev.GroupName,
				From: ev.FromUID, EventId: ev.ID,
			})
		case ActionGrpDisband:
			client.Unsubscribe(ev.Topic)
			client.SendAny(&pendingGroupEventPush{
				Action: ActionGrpDisband, Topic: ev.Topic,
				From: ev.FromUID, EventId: ev.ID,
			})
		case PendingGroupEventJoinRequest:
			// No re-check of ensureGroupManager here -- if this uid's role
			// changed since the request was queued, the real authorization
			// gate is ensureGroupManager on their next group_join_requests_list
			// call, not this "go check" nudge.
			client.SendAny(&pendingGroupEventPush{
				Action: ActionGroupJoinRequestPush, Topic: ev.Topic, Name: ev.GroupName,
				From: ev.FromUID, EventId: ev.ID,
			})
		case PendingGroupEventJoinApproved, PendingGroupEventJoinRejected:
			status := "approved"
			if ev.Kind == PendingGroupEventJoinRejected {
				status = "rejected"
			}
			client.SendAny(&pendingGroupEventPush{
				Action: ActionGroupJoinReviewed, Topic: ev.Topic, Name: ev.GroupName,
				EventId: ev.ID, JoinStatus: status,
			})
		default:
			logf("deliverPendingGroupEvents: unknown kind %q on event %d, dropping", ev.Kind, ev.ID)
			if ackErr := h.store.DropPendingGroupEvent(ev.ID, client.uid); ackErr != nil {
				logf("deliverPendingGroupEvents: drop unknown event %d: %v", ev.ID, ackErr)
			}
		}
	}
}

// handleAckPendingGroupEvent 与 handleAckTopicCleared 一样是 fire-and-forget：
// 客户端发这条时本地状态已经改完了，删不删得掉队列行只影响它什么时候被回收，
// 不影响正确性（下次登录重放一遍是幂等的）。
func (h *Hub) handleAckPendingGroupEvent(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.EventID <= 0 {
		return
	}
	if err := h.store.AckPendingGroupEvent(msg.EventID, client.uid, client.Device()); err != nil {
		logf("handleAckPendingGroupEvent: %v", err)
	}
}
