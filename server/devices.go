// Several devices per user (task 1.12): connection lookup, fan-out to a
// user's devices, and deciding which devices still need a change queued.

package main

import (
	"encoding/json"
)

// A login may name its device ("device"). A user can be connected from any
// number of devices at once; a new login replaces only the connection of the
// same device. A login without a device is the device "", so clients that
// never send one keep the reference behaviour: each login replaces the last.
//
// The field is "device", not "deviceId": deviceId is already the E2EE
// (Signal) device number on the bundle frames.

// maxDeviceLen bounds a device id; it is stored in VARCHAR(64) columns.
const maxDeviceLen = 64

// validDevice accepts letters, digits and . _ : - up to maxDeviceLen.
func validDevice(d string) bool {
	if len(d) > maxDeviceLen {
		return false
	}
	for _, r := range d {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == ':', r == '-':
		default:
			return false
		}
	}
	return true
}

// connKey names one device of one user: the slot a connection holds in the
// connection table and the topic index.
type connKey struct{ uid, device string }

// clientsOf returns the user's connections, one per device.
func (h *Hub) clientsOf(uid string) []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	devices := h.clients[uid]
	out := make([]*Client, 0, len(devices))
	for _, c := range devices {
		out = append(out, c)
	}
	return out
}

// clientOf returns the connection of one device of a user, or nil.
func (h *Hub) clientOf(uid, device string) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clients[uid][device]
}

// isOnline reports whether the user has a connection on any device.
func (h *Hub) isOnline(uid string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[uid]) > 0
}

// onlineDevices lists the devices the user is connected from.
func (h *Hub) onlineDevices(uid string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.clients[uid]))
	for d := range h.clients[uid] {
		out = append(out, d)
	}
	return out
}

// deviceSlotFree reports whether uid may log in from device: it is already
// connected from that device (the login replaces it), or from fewer than
// maxDevicesPerUser devices.
func (h *Hub) deviceSlotFree(uid, device string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	devices := h.clients[uid]
	if _, ok := devices[device]; ok {
		return true
	}
	return len(devices) < maxDevicesPerUser
}

// sameDevice reports whether c is a connection of except's device: except
// itself, or the connection that replaced it. The acting device is skipped by
// device, not by connection, so a late frame from a replaced connection does
// not echo the action back to the device that made it -- with one device per
// user this is the reference server's skipping of the acting user.
func sameDevice(c, except *Client) bool {
	return except != nil && c.key() == except.key()
}

// sendToUser sends v to every connection of uid except those of one device
// (nil for none) and returns the devices it reached.
func (h *Hub) sendToUser(uid string, v interface{}, except *Client) []string {
	clients := h.clientsOf(uid)
	if len(clients) == 0 {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		logf("sendToUser: marshal failed for %s: %v", uid, err)
		return nil
	}
	var reached []string
	for _, c := range clients {
		if sameDevice(c, except) {
			continue
		}
		c.SendRaw(data)
		reached = append(reached, c.Device())
	}
	return reached
}

// subscribeUser puts every connection of uid except those of one device on
// topic, and reports whether the user has any connection at all.
func (h *Hub) subscribeUser(uid, topic string, except *Client) bool {
	clients := h.clientsOf(uid)
	for _, c := range clients {
		if !sameDevice(c, except) {
			c.Subscribe(topic)
		}
	}
	return len(clients) > 0
}

// queueMissing queues a change for each user in uids who has a device that
// did not get it live: a user no device of whom was reached, or one with a
// device seen in the last 30 days that is not among those reached. reached
// maps a user to the devices that got the change (the acting device
// included); enqueue stores one queue row with those devices as already
// having it.
//
// A user with a single device behaves as in the reference server: queued
// when not connected, not queued when connected.
func (h *Hub) queueMissing(uids []string, reached map[string][]string, enqueue func(uid string, delivered []string)) {
	var partly []string
	for _, uid := range uids {
		if len(reached[uid]) == 0 {
			enqueue(uid, nil)
		} else {
			partly = append(partly, uid)
		}
	}
	if len(partly) == 0 {
		return
	}
	known, err := h.store.KnownDevices(partly)
	if err != nil {
		// The connected devices have it; only devices that are away miss it.
		logf("queueMissing: KnownDevices failed: %v", err)
		return
	}
	for _, uid := range partly {
		got := make(map[string]bool, len(reached[uid]))
		for _, d := range reached[uid] {
			got[d] = true
		}
		for _, d := range known[uid] {
			if !got[d] {
				enqueue(uid, reached[uid])
				break
			}
		}
	}
}

// groupEvent is a membership change sent to a user's devices and, for
// devices that are away, queued in im_pending_group_events.
type groupEvent struct {
	topic string
	kind  string // the queue row's kind
	name  string // the queue row's group name
	from  string
	frame interface{} // what a connected device gets
	// membership is what the change does to the devices' subscription to
	// topic: subscribe (+1), unsubscribe (-1) or nothing (0).
	membership int
	// queue is false for the join-flow events the reference server never
	// queued (D9); they are queued under the strict profile only.
	queue bool
}

// deliverGroupEvent sends ev to every device of each user and queues it for
// the devices that were away. actor is the connection that made the change:
// it got the reply, so its device is skipped and counts as having the event.
func (h *Hub) deliverGroupEvent(ev groupEvent, uids []string, actor *Client) {
	actorUID := ""
	if actor != nil {
		actorUID = actor.UID()
	}
	reached := make(map[string][]string, len(uids))
	for _, uid := range uids {
		var got []string
		var except *Client
		if uid == actorUID {
			except = actor
			got = append(got, actor.Device())
		}
		for _, c := range h.clientsOf(uid) {
			if sameDevice(c, except) {
				continue
			}
			switch ev.membership {
			case 1:
				c.Subscribe(ev.topic)
			case -1:
				c.Unsubscribe(ev.topic)
			}
			c.SendAny(ev.frame)
			got = append(got, c.Device())
		}
		reached[uid] = got
	}
	if !ev.queue {
		return
	}
	h.queueMissing(uids, reached, func(uid string, delivered []string) {
		if err := h.store.EnqueuePendingGroupEvent(ev.topic, uid, ev.kind, ev.name, ev.from, delivered); err != nil {
			logf("deliverGroupEvent: topic=%s uid=%s kind=%s: %v", ev.topic, uid, ev.kind, err)
		}
	})
}
