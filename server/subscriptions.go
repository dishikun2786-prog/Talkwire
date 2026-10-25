// Subscription index and topic broadcast.

package main

import (
	"encoding/json"
)

func (h *Hub) handleSub(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionSub, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionSub, CodeBadReq, "topic required"))
		return
	}
	if msg.Topic == TopicMe {
		client.Subscribe(msg.Topic)
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionSub, Code: CodeOK, Topic: msg.Topic})
		return
	}
	if msg.Topic == TopicFnd {
		client.SendJSON(ErrorResponse(msg.Id, ActionSub, CodeBadReq, "fnd not supported"))
		return
	}

	// P2P topics are addressed by the peer's UID on the wire, but broadcasts
	// are matched against the canonical p2p_<hash> topic (see handlePub/handleGet).
	// Without this resolution, subscribing here recorded the subscription under
	// the wrong key and the peer never received real-time messages/presence
	// until they happened to pub/get in the same session.
	actualTopic := msg.Topic
	isP2P := isUserUID(msg.Topic)
	peerOnline := false
	if isP2P {
		var ok bool
		actualTopic, peerOnline, ok = h.resolveP2PTopic(client, msg.Id, ActionSub, msg.Topic)
		if !ok {
			return
		}
	} else if !h.ensureTopicMember(client, msg, ActionSub, actualTopic) {
		return
	}

	client.Subscribe(actualTopic)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionSub, Code: CodeOK, Topic: msg.Topic})
	if isP2P {
		// Presence is otherwise only pushed at the instant a peer connects or
		// disconnects, and only to clients already online at that moment — a
		// client subscribing later would never learn the peer's current
		// status. Send an explicit snapshot right after subscribing.
		status := PresOffline
		if peerOnline {
			status = PresOnline
		}
		client.SendJSON(&ServerMessage{
			Action: ActionPres, Topic: msg.Topic, What: status,
			Online: peerOnline, PresRev: h.currentPresRev(msg.Topic),
		})
	}
	logf("[Sub] %s -> %s (%s)", client.uid, msg.Topic, actualTopic)
}

func (h *Hub) handleLeave(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Topic == "" {
		return
	}
	client.Unsubscribe(msg.Topic)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionLeave, Code: CodeOK, Topic: msg.Topic})
}

// subscribeTopic registers client under topic in the hub's topic index, so
// BroadcastToTopic can reach it in O(topic size) instead of scanning every
// connected client.
func (h *Hub) subscribeTopic(topic string, client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.topics[topic]
	if !ok {
		m = make(map[string]*Client)
		h.topics[topic] = m
	}
	m[client.uid] = client
}

// unsubscribeTopic removes client from the topic index, dropping the topic
// entry entirely once its last subscriber leaves.
func (h *Hub) unsubscribeTopic(topic string, client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m, ok := h.topics[topic]; ok {
		delete(m, client.uid)
		if len(m) == 0 {
			delete(h.topics, topic)
		}
	}
}

// BroadcastToTopic sends msg to every client currently subscribed to topic,
// via the topic index rather than scanning every connected client — the
// previous version cost O(total online users) per message regardless of how
// many people were actually in the conversation.
func (h *Hub) BroadcastToTopic(topic string, msg *ServerMessage, excludeUID string) {
	h.broadcastRaw(topic, msg, excludeUID)
}

// BroadcastAny is BroadcastToTopic's counterpart for the "type"-discriminated
// wire structs (AiTypingMessage, ...) that don't fit ServerMessage's shape —
// mirrors the SendJSON/SendAny split on Client.
func (h *Hub) BroadcastAny(topic string, msg interface{}, excludeUID string) {
	h.broadcastRaw(topic, msg, excludeUID)
}

// broadcastRaw is the shared body of BroadcastToTopic/BroadcastAny.
//
// F-12/F-13：改前两者都在**持着 h.mu 的循环里**对每个收件人各 SendJSON 一次，
// 而 SendJSON/SendAny 内部各自 json.Marshal 一遍同一个对象。一条 500 人群的
// 消息因此要序列化 500 次相同的 JSON，全程占着那把全局读锁 —— 而
// subscribeTopic / unsubscribeTopic / Run 的注册注销分支要的是写锁，会被整段
// 排在后面。序列化一次、循环里只做 chan 投递，把锁内工作量压回 O(收件人数)
// 的纯内存写。
//
// marshal 刻意放在取锁**之前**：它是这里唯一可能耗时的一步，没有任何理由
// 在锁内做。
func (h *Hub) broadcastRaw(topic string, v interface{}, excludeUID string) {
	data, err := json.Marshal(v)
	if err != nil {
		logf("broadcastRaw: marshal failed for topic=%s: %v", topic, err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for uid, client := range h.topics[topic] {
		if uid != excludeUID {
			client.SendRaw(data)
		}
	}
}

// isSubscribedInIndex 报告这条**连接本身**是否已登记在 h.topics[topic] 里。
//
// 比的是 client 指针而不是 uid：重连期间同一个 uid 短暂存在两个 *Client，按
// uid 判断会让已经被顶替的旧连接也算「已订阅」。这与 Run 的 unregister 分支
// 里那条「必须按连接身份而非 uid 清理」是同一条教训。
func (h *Hub) isSubscribedInIndex(topic string, client *Client) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	m, ok := h.topics[topic]
	if !ok {
		return false
	}
	return m[client.uid] == client
}
