// Conversation change summary: sync_summary (strict profile, capability
// sync-summary; protocol specification 8.1).

package main

// handleSyncSummary answers sync_summary with every conversation the caller
// is in and what a reconnecting client needs to decide whether it has to pull
// it: the last seq, the clear watermark, the caller's own read position, the
// other member of a direct chat, and for a group with read receipts on, when
// another member last moved a read cursor. A client then sends get only for
// the conversations that changed, instead of one get per conversation.
//
// It also subscribes the connection to every one of them, as a get does, so a
// client that skips the get of an unchanged conversation still receives its
// live frames. The subscriptions come before the summary is read: a message
// committed after the read reaches the connection as a push, and one
// committed before it is counted in lastSeq. One caught by both is pulled and
// pushed, and the client drops the second copy by its seq.
//
// legacy answers like the reference server, which has no such action.
func (h *Hub) handleSyncSummary(client *Client, msg *ClientMessage) {
	if !strictProtocol {
		client.SendJSON(ErrorResponse(msg.Id, msg.Action, CodeBadReq, "unknown action: "+msg.Action))
		return
	}
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionSyncSummary, CodeAuth, "not authenticated"))
		return
	}
	topics, err := h.store.GetUserTopicNames(client.uid)
	if err != nil {
		logf("GetUserTopicNames(%s): %v", client.uid, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionSyncSummary, CodeInternal, "retrieve failed"))
		return
	}
	for _, t := range topics {
		client.Subscribe(t)
	}
	summary, err := h.store.GetSyncSummary(client.uid, CurrentRuntimeConfig().ReadReceiptEnabled)
	if err != nil {
		logf("GetSyncSummary(%s): %v", client.uid, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionSyncSummary, CodeInternal, "retrieve failed"))
		return
	}
	// No conversations: topics is omitted, as get_groups omits groups.
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionSyncSummary, Code: CodeOK, Topics: summary})
}
