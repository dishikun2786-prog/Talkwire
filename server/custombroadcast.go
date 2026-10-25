// The custom-broadcast host endpoint: relays a host card update to the
// connected clients (protocol specification, appendix B).

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

// customKind is the host's name for a card type, sent as the frame's "what".
var customKind = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// maxCustomBroadcastBody bounds one request. A card update carries the
// card's current state, not its history.
const maxCustomBroadcastBody = 64 << 10

// InternalCustomBroadcast relays payload, uninterpreted, as
// {"action":"custom_update","topic":...,"what":kind,"extra":payload} to the
// connections subscribed to topic, or, given targetUid instead, to every
// connection of that user (no "topic" then). It is the general form of
// red-packet-broadcast: the host owns the card's state and decides what kind
// means; this server only reaches the live sockets, which exist in this
// process alone. Nothing is queued for clients that are not connected, so the
// host must also let a client fetch a card's current state.
func (h *WebSocketHandler) InternalCustomBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireInternalService(w, r) {
		return
	}
	var req struct {
		Topic     string          `json:"topic"`
		TargetUID string          `json:"targetUid"`
		Kind      string          `json:"kind"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCustomBroadcastBody)).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeInternalJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "body too large"})
			return
		}
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	topic, target := strings.TrimSpace(req.Topic), strings.TrimSpace(req.TargetUID)
	switch {
	case (topic == "") == (target == ""):
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "exactly one of topic and targetUid required"})
		return
	case !customKind.MatchString(req.Kind):
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid kind"})
		return
	case len(req.Payload) == 0 || bytes.Equal(req.Payload, []byte("null")):
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "payload required"})
		return
	}
	msg := &ServerMessage{Action: "custom_update", Topic: topic, What: req.Kind, Extra: req.Payload}
	if topic != "" {
		h.hub.BroadcastToTopic(topic, msg, "")
		writeInternalJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	delivered := false
	for _, c := range h.hub.clientsOf(target) {
		c.SendJSON(msg)
		delivered = true
	}
	writeInternalJSON(w, http.StatusOK, map[string]bool{"ok": true, "delivered": delivered})
}
