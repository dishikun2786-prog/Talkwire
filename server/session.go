// Connection lifecycle: register, unregister and login.

package main

import (
	"time"
)

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			uid, device := client.UID(), client.Device()
			var victim *Client
			h.mu.Lock()
			devices := h.clients[uid]
			if devices == nil {
				devices = make(map[string]*Client)
				h.clients[uid] = devices
			}
			// Only a login from the same device replaces a connection.
			if existing, ok := devices[device]; ok && existing != client {
				victim = existing
			}
			devices[device] = client
			total := len(h.clients)
			h.mu.Unlock()

			// This uid is live again -- cancel any scheduleForceEndCallForDisconnect
			// grace timer a previous disconnect left pending for it. Runs
			// unconditionally on every register, not just reconnects with a
			// victim below: a brand new login for a uid that was never in a
			// call is simply a no-op inside cancelPendingCallDisconnect.
			h.mods.connected(h, uid, device)

			// Disconnect() blocking-sends on h.unregister, and the ONLY
			// receiver of that channel is this very Run loop. Calling it
			// inline — worse, while holding h.mu, as this used to — lets a
			// mass-reconnect storm fill the 100-slot buffer and wedge Run
			// forever *while holding the global lock*: subscribeTopic,
			// BroadcastToTopic, BroadcastPresence and GetClientByUID all
			// stall behind it and the whole service goes silently dead with
			// no error and no log. Hand the kick to another goroutine and
			// never hold the lock across it. (conn.Close() inside Disconnect
			// is a blocking syscall too, which is the second reason.)
			if victim != nil {
				logf("Kicking existing session for %s", client.uid)
				go func(v *Client) {
					v.SendJSON(&ServerMessage{
						Action: "login",
						Code:   CodeAuth,
						Reason: "logged in from another device",
					})
					v.Disconnect()
				}(victim)
			}

			// Every login announces "on", as in the reference server (a
			// bystander sees "on" again when a connection is replaced).
			h.enqueuePresence(uid, PresOnline)
			logf("Client registered: %s device %q (users online: %d)", uid, device, total)

		case client := <-h.unregister:
			topics := client.drainSubscriptions()
			uid, device := client.UID(), client.Device()
			key := connKey{uid, device}
			lastDevice := false
			h.mu.Lock()
			// A reconnecting user briefly has two live *Client objects for one
			// uid, and the superseded one unregisters *after* the replacement
			// has already registered and re-subscribed. So every cleanup below
			// must key on connection IDENTITY, never on the uid string alone.
			// (h.clients[uid] missing entirely also lands here: either the
			// replacement already unregistered, or this client never got in —
			// both mean "not the live connection", which is what we want.)
			devices := h.clients[uid]
			superseded := devices[device] != client
			if !superseded {
				delete(devices, device)
				if len(devices) == 0 {
					delete(h.clients, uid)
					lastDevice = true
				}
			}
			for _, topic := range topics {
				if m, ok := h.topics[topic]; ok {
					// Unguarded, this deleted by uid and therefore evicted the
					// *replacement* connection from every shared topic. Since
					// h.topics is the sole fan-out source for both
					// BroadcastToTopic and BroadcastPresence, the survivor then
					// silently lost real-time messages AND presence while still
					// believing it was subscribed (its `sub` had returned 200,
					// and the client's knownTopics guard suppresses any re-sub),
					// with only a full re-login as repair.
					if m[key] == client {
						delete(m, key)
						if len(m) == 0 {
							delete(h.topics, topic)
						}
					}
				}
			}
			h.mu.Unlock()

			if uid == "" {
				break
			}
			if superseded {
				// Announcing "offline" here is how a user who had just
				// reconnected got shown as offline to all their peers, with
				// nothing in the system able to correct it afterwards.
				logf("Client unregistered (superseded by newer session, presence unchanged): %s device %q", uid, device)
				break
			}
			go func() {
				if err := h.store.EnsureDevice(uid, device); err != nil {
					logf("EnsureDevice(%s, %q) on logout: %v", uid, device, err)
				}
			}()
			if !lastDevice {
				// The user is still online on another device: no presence
				// change. The modules still hear that this device left (a
				// call on it must end).
				logf("Client unregistered: %s device %q (other devices online)", uid, device)
				h.mods.disconnected(h, uid, device)
			} else {
				h.enqueuePresence(uid, PresOffline)
				logf("Client unregistered: %s device %q (users online: %d)", uid, device, len(h.clients))
				// Deliberately inside the !superseded branch, same "connection
				// identity, not uid string" discipline as the h.topics cleanup
				// a few lines up (IM_VOICE_VIDEO_CALL_PLAN.md §4.3): a mere
				// reconnect (new connection already registered under this uid,
				// old one now unwinding through here as the victim) must NOT
				// force-end a call this uid is legitimately still in -- only a
				// genuine "this uid has no live connection left at all" should.
				// callState never holds a *Client, only uid strings, so the
				// call survives the reconnect untouched either way; this only
				// runs at all when the uid is truly gone -- and even then,
				// not immediately: scheduleForceEndCallForDisconnect gives a
				// real reconnect (OEM background-throttling teardown/rebuild,
				// not just this instant-supersede case) a grace period to
				// land before actually ending anything. See its own doc
				// comment for why the ~5-second-scale window this teardown/
				// rebuild takes on some OEM skins can't be caught by the
				// supersede check above alone.
				h.mods.disconnected(h, uid, device)
			}
		}
	}
}

func (h *Hub) handleLogin(client *Client, msg *ClientMessage) {
	// M19: without this guard, a client sending a second "login" on an
	// already-authenticated connection overwrites client.uid in place.
	// h.register's `existing != client` check (see Run()) only catches a
	// DIFFERENT connection claiming a uid that's already registered — it
	// does nothing to protect against the SAME connection switching
	// identities, because the old uid was never re-registered through that
	// path to begin with. The old uid's h.clients entry and every
	// h.topics[topic][oldUid] subscription this connection made stay
	// indexed under the abandoned identity forever: eventual disconnect
	// cleanup (h.unregister) only looks under client.uid's CURRENT value,
	// so nothing ever revisits them. A stale h.clients[oldUid] entry then
	// makes GetClientByUID(oldUid) return this connection as if that user
	// were still online and reachable here — messages/pushes meant for the
	// old identity could be delivered down a socket now authenticated as
	// someone else entirely.
	if client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionLogin, CodeBadReq, "already logged in"))
		return
	}
	if msg.Token == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionLogin, CodeAuth, "token required"))
		return
	}
	if !validDevice(msg.Device) {
		client.SendJSON(ErrorResponse(msg.Id, ActionLogin, CodeBadReq, "invalid device"))
		return
	}
	appUserID, err := h.auth.UserID(msg.Token)
	if err != nil {
		logf("Login failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionLogin, CodeAuth, "invalid token"))
		return
	}
	imUID, err := h.store.EnsureUser(appUserID)
	if err != nil {
		logf("EnsureUser failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionLogin, CodeInternal, "server error"))
		return
	}
	if !h.deviceSlotFree(imUID, msg.Device) {
		logf("Login refused: %s already connected from %d devices", imUID, maxDevicesPerUser)
		client.SendJSON(ErrorResponse(msg.Id, ActionLogin, CodeConflict, "too many devices"))
		return
	}
	// 本次审查顺带发现并修复：这两行此前都是绕过 c.mu 的裸字段赋值，即使
	// UID()/setUID() 已经存在（F-39 的修复），登录处理本身却从未真正改用它——
	// 意味着 F-39 只修了"读"，没有修"写"，读侧的锁保护不了一个仍然不加锁的
	// 写入者，本质上还是同一类数据竞争。appUserID 是本次通话功能新增的字段，
	// 一并按 setUID/AppUserID() 的同款模式补上 setAppUserID()（见 client.go）。
	client.setUID(imUID)
	client.setAppUserID(appUserID)
	client.setDevice(msg.Device)
	// Known devices decide which changes are queued for a device that is
	// away (see queueMissing). A failure here only loses that.
	if err := h.store.EnsureDevice(imUID, msg.Device); err != nil {
		logf("EnsureDevice(%s, %q) failed: %v", imUID, msg.Device, err)
	}
	client.loginTime = time.Now()
	// The generation and the queue position are taken together, so the
	// order Run registers connections in is the order of their generations
	// (F60, see subscribeTopic).
	h.loginMu.Lock()
	h.loginGen++
	client.gen.Store(h.loginGen)
	h.register <- client
	h.loginMu.Unlock()
	client.SendJSON(h.loginReply(msg.Id, imUID))
	logf("User logged in: %s -> %s device %q", appUserID, imUID, msg.Device)
	go h.deliverPendingClears(client)
	go h.deliverPendingGroupEvents(client)
	h.mods.afterLogin(h, client)
}
