// Scan-to-join (optional module): QR tokens, join requests and review.

package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"example.com/imkit/server/migrate"
	"example.com/imkit/server/migrations"
	"example.com/imkit/server/push"
)

// handleGroupQrToken mints-or-returns a group's current scan-to-join QR
// token. Viewing/sharing the current token is open to any member (mirrors
// WeChat: anyone in the group can bring someone in); RefreshQr=true
// additionally requires ensureGroupManager because invalidating the link
// everyone else may currently be relying on is a moderation-tier action,
// same tier as kick/rename, not a plain view.
func (h *Hub) handleGroupQrToken(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrToken, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrToken, CodeBadReq, "topic required"))
		return
	}
	if !h.ensureTopicMember(client, msg, ActionGroupQrToken, msg.Topic) {
		return
	}
	if msg.RefreshQr && !h.ensureGroupManager(client, msg, ActionGroupQrToken, msg.Topic) {
		return
	}
	var tok *GroupInviteToken
	var err error
	if !msg.RefreshQr {
		tok, err = h.store.GetActiveGroupQrToken(msg.Topic)
		if err != nil {
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrToken, CodeInternal, "获取二维码失败"))
			return
		}
	}
	if tok == nil {
		ttl := time.Duration(CurrentRuntimeConfig().QrTtlSeconds) * time.Second
		tok, err = h.store.MintGroupQrToken(msg.Topic, client.uid, ttl)
		if err != nil {
			logf("handleGroupQrToken: mint failed: %v", err)
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrToken, CodeInternal, "生成二维码失败"))
			return
		}
	}
	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionGroupQrToken, Code: CodeOK, Topic: msg.Topic,
		QrToken: tok.Token, ExpiresAt: tok.ExpiresAt.UnixMilli(),
	})
}

// handleGroupQrResolve previews a scanned token's group before the caller
// decides to join/apply. Deliberately the one group_* action open to a
// non-member -- resolving before joining is the entire point of scanning a
// code you found on someone else's screen.
func (h *Hub) handleGroupQrResolve(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeAuth, "not authenticated"))
		return
	}
	if msg.QrToken == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeBadReq, "qrToken required"))
		return
	}
	topic, ok, err := h.store.ResolveGroupQrToken(msg.QrToken)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeInternal, "二维码校验失败"))
		return
	}
	if !ok {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeNotFound, "二维码不存在或已过期"))
		return
	}
	info, err := h.store.GetGroupInfo(topic)
	if err != nil || info == nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeNotFound, "群聊不存在"))
		return
	}
	alreadyMember, err := h.store.IsGroupMember(topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeInternal, "校验失败"))
		return
	}
	settings, err := h.store.GetGroupJoinSettings(topic)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeInternal, "校验失败"))
		return
	}
	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionGroupQrResolve, Code: CodeOK, Topic: topic, Name: info.Name,
		MemberCount: info.Members, RequireApproval: settings.RequireApproval,
		AlreadyMember: alreadyMember, IsFull: info.Members >= CurrentRuntimeConfig().MaxMembers,
	})
}

// handleGroupJoin is the single entry point for scan-to-join, whether or not
// the group requires approval -- the server re-resolves the token and
// re-decides based on the group's *current* settings rather than trusting
// whatever an earlier group_qr_resolve reply told the client, and answers
// via JoinStatus so the client never has to pre-guess which branch will run.
//
// Joining this way deliberately skips handleInvite's AreFriends check: a
// scan-to-join code has no existing member vouching for the newcomer by
// definition, which is the whole point of the feature (same relaxation
// WeChat's own group QR makes) -- but the MaxMembers cap still applies
// exactly as it does for invite.
func (h *Hub) handleGroupJoin(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeAuth, "not authenticated"))
		return
	}
	if msg.QrToken == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeBadReq, "qrToken required"))
		return
	}
	topic, ok, err := h.store.ResolveGroupQrToken(msg.QrToken)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeInternal, "二维码校验失败"))
		return
	}
	if !ok {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeNotFound, "二维码不存在或已过期"))
		return
	}
	already, err := h.store.IsGroupMember(topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeInternal, "校验失败"))
		return
	}
	if already {
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoin, Code: CodeOK, Topic: topic, JoinStatus: "joined"})
		return
	}
	info, err := h.store.GetGroupInfo(topic)
	if err != nil || info == nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeNotFound, "群聊不存在"))
		return
	}
	if info.Members+1 > CurrentRuntimeConfig().MaxMembers {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeConflict, "群成员数量已达上限"))
		return
	}
	settings, err := h.store.GetGroupJoinSettings(topic)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeInternal, "校验失败"))
		return
	}
	if settings.RequireApproval {
		message := msg.JoinMessage
		if runes := []rune(message); len(runes) > 60 {
			message = string(runes[:60])
		}
		if _, err := h.store.CreateGroupJoinRequest(topic, client.uid, message); err != nil {
			if errors.Is(err, ErrDuplicateJoinRequest) {
				client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoin, Code: CodeOK, Topic: topic, JoinStatus: "pending_approval"})
				return
			}
			logf("handleGroupJoin: create request failed: %v", err)
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeInternal, "申请提交失败"))
			return
		}
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoin, Code: CodeOK, Topic: topic, JoinStatus: "pending_approval"})
		h.notifyGroupManagersOfJoinRequest(topic, client.uid, info.Name)
		return
	}
	if err := h.store.AddGroupMember(topic, client.uid); err != nil {
		logf("handleGroupJoin: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeInternal, "加入失败"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoin, Code: CodeOK, Topic: topic, JoinStatus: "joined"})
	logf("[Group] %s joined %s via QR (no approval required)", client.uid, topic)
}

// handleGroupJoinRequestsList lists a group's pending scan-to-join requests
// for its owner/admins, resolving each requester's display name the same
// way pushOfflineRecipients resolves a sender's -- per-viewer, since the
// reviewer may have set a personal alias for the requester (see
// Contact.alias).
func (h *Hub) handleGroupJoinRequestsList(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinRequestsList, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinRequestsList, CodeBadReq, "topic required"))
		return
	}
	if !h.ensureGroupManager(client, msg, ActionGroupJoinRequestsList, msg.Topic) {
		return
	}
	reqs, err := h.store.GetPendingGroupJoinRequests(msg.Topic)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinRequestsList, CodeInternal, "查询失败"))
		return
	}
	dtos := make([]GroupJoinRequestDto, 0, len(reqs))
	if len(reqs) == 0 {
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoinRequestsList, Code: CodeOK, Topic: msg.Topic, JoinRequests: dtos})
		return
	}
	viewerAppUserID, verr := h.store.GetAppUserID(client.uid)
	for _, r := range reqs {
		name := r.RequesterUID
		if verr == nil && viewerAppUserID != "" {
			if requesterAppUserID, aerr := h.store.GetAppUserID(r.RequesterUID); aerr == nil && requesterAppUserID != "" {
				if resolvedName, _, gerr := h.host.GetProfile(viewerAppUserID, requesterAppUserID); gerr == nil && resolvedName != "" {
					name = resolvedName
				}
			}
		}
		dtos = append(dtos, GroupJoinRequestDto{
			ID: r.ID, RequesterUID: r.RequesterUID, RequesterName: name,
			Message: r.Message, CreatedAt: r.CreatedAt.UnixMilli(),
		})
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoinRequestsList, Code: CodeOK, Topic: msg.Topic, JoinRequests: dtos})
}

// handleGroupJoinReview approves or rejects one pending request.
// ReviewGroupJoinRequest's WHERE status='pending' clause is the concurrency
// guard: if two admins review the same request at once, the loser gets nil
// back here and must be told "already handled", not silently re-apply their
// own verdict on top.
func (h *Hub) handleGroupJoinReview(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.JoinRequestID <= 0 {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeBadReq, "topic and joinRequestId required"))
		return
	}
	if !h.ensureGroupManager(client, msg, ActionGroupJoinReview, msg.Topic) {
		return
	}
	req, err := h.store.ReviewGroupJoinRequest(msg.JoinRequestID, msg.Approve, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeInternal, "处理失败"))
		return
	}
	if req == nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeConflict, "该申请已被处理"))
		return
	}
	// ensureGroupManager only checked authority over msg.Topic, not over
	// whatever topic joinRequestId actually belongs to -- without this check
	// a manager of group A could review (and learn the existence of) a
	// request against group B just by guessing an id.
	if req.Topic != msg.Topic {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeBadReq, "请求与群聊不匹配"))
		return
	}
	status := "rejected"
	if msg.Approve {
		status = "approved"
		info, err := h.store.GetGroupInfo(msg.Topic)
		if err != nil || info == nil {
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeNotFound, "群聊不存在"))
			return
		}
		if info.Members+1 > CurrentRuntimeConfig().MaxMembers {
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeConflict, "群成员数量已达上限"))
			return
		}
		if err := h.store.AddGroupMember(msg.Topic, req.RequesterUID); err != nil {
			logf("handleGroupJoinReview: add member failed: %v", err)
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeInternal, "处理失败"))
			return
		}
		h.deliverGroupEvent(groupEvent{
			topic: msg.Topic, kind: ActionGrpSync, name: info.Name, from: client.uid,
			frame:      &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, Name: info.Name, From: client.uid},
			membership: 1, queue: true,
		}, []string{req.RequesterUID}, client)
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoinReview, Code: CodeOK, Topic: msg.Topic, JoinStatus: status})
	h.notifyRequesterOfReview(msg.Topic, req.RequesterUID, status)
}

func (h *Hub) handleGetGroupJoinSettings(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetGroupJoinSettings, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetGroupJoinSettings, CodeBadReq, "topic required"))
		return
	}
	if !h.ensureTopicMember(client, msg, ActionGetGroupJoinSettings, msg.Topic) {
		return
	}
	settings, err := h.store.GetGroupJoinSettings(msg.Topic)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetGroupJoinSettings, CodeInternal, "查询失败"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGetGroupJoinSettings, Code: CodeOK, Topic: msg.Topic, RequireApproval: settings.RequireApproval})
}

// handleUpdateGroupJoinSettings is owner-only, same precedent and same
// reasoning as handleUpdateGroupAiSettings: this toggle can leave a group
// without the protection it exists to provide if an admin could silently
// flip it off, which is the settings-toggle risk shape, not the
// moment-to-moment moderation shape of kick/rename (ensureGroupManager).
func (h *Hub) handleUpdateGroupJoinSettings(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGroupJoinSettings, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGroupJoinSettings, CodeBadReq, "topic required"))
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGroupJoinSettings, CodeNotFound, "群聊不存在"))
		return
	}
	if !isOwner {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGroupJoinSettings, CodeAuth, "仅群主可以设置"))
		return
	}
	if err := h.store.UpsertGroupJoinSettings(msg.Topic, msg.RequireApproval, client.uid); err != nil {
		logf("handleUpdateGroupJoinSettings: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGroupJoinSettings, CodeInternal, "设置失败"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionUpdateGroupJoinSettings, Code: CodeOK, Topic: msg.Topic, RequireApproval: msg.RequireApproval})
}

// notifyGroupManagersOfJoinRequest tells a group's owner and admins that a
// new scan-to-join request is awaiting review -- live push if connected,
// offline queue + best-effort FCM otherwise. Carries no request details
// itself (same "go refresh" shape as ActionGrpSync): a client always
// re-fetches via group_join_requests_list rather than trusting a snapshot
// embedded in the push.
func (h *Hub) notifyGroupManagersOfJoinRequest(topic, requesterUID, groupName string) {
	managers, err := h.store.GetGroupManagerUIDs(topic)
	if err != nil {
		logf("notifyGroupManagersOfJoinRequest: GetGroupManagerUIDs failed: %v", err)
		return
	}
	var notify []string
	for _, uid := range managers {
		if uid != requesterUID {
			notify = append(notify, uid)
		}
	}
	if len(notify) == 0 {
		return
	}
	// D9: the reference server tried to queue this and always failed (the
	// store accepted only grp_sync and grp_disband), so an offline manager
	// never heard of the request. Queued under the strict profile only, where
	// deliverPendingGroupEvents replays it at their next login.
	h.deliverGroupEvent(groupEvent{
		topic: topic, kind: PendingGroupEventJoinRequest, name: groupName, from: requesterUID,
		frame: &ServerMessage{Action: ActionGroupJoinRequestPush, Topic: topic, Name: groupName, From: requesterUID},
		queue: strictProtocol,
	}, notify, nil)
	text := CurrentRuntimeConfig().PushText
	h.pushJoin(notify, groupName, text.group(text.JoinRequest, groupName), topic)
}

// notifyRequesterOfReview tells an applicant whether their request was
// approved or rejected -- live push if online, offline queue + best-effort
// FCM otherwise.
func (h *Hub) notifyRequesterOfReview(topic, requesterUID, status string) {
	groupName := h.groupNameCached(topic)
	kind := PendingGroupEventJoinRejected
	text := CurrentRuntimeConfig().PushText
	body := text.group(text.JoinRejected, groupName)
	if status == "approved" {
		kind = PendingGroupEventJoinApproved
		body = text.group(text.JoinApproved, groupName)
	}
	// D9, as in notifyGroupManagersOfJoinRequest: queued under the strict profile.
	h.deliverGroupEvent(groupEvent{
		topic: topic, kind: kind, name: groupName,
		frame: &ServerMessage{Action: ActionGroupJoinReviewed, Topic: topic, Name: groupName, JoinStatus: status},
		queue: strictProtocol,
	}, []string{requesterUID}, nil)
	h.pushJoin([]string{requesterUID}, groupName, body, topic)
}

// pushJoin is a lighter-weight sibling of pushOfflineRecipients for the
// small recipient sets these two join-flow notifications ever address (a
// group's manager roster is capped at CurrentRuntimeConfig().MaxAdmins+1;
// a review notice has exactly one recipient) -- no goroutine/semaphore pool
// needed at this scale, unlike a 500-member group's message fan-out.
func (h *Hub) pushJoin(uids []string, groupName, body, topic string) {
	rc := CurrentRuntimeConfig()
	if !rc.FCMEnabled || h.pusher == nil {
		return
	}
	targets := h.pushTargets(uids, "pushJoin")
	n := push.Notification{Kind: push.KindGroupJoin, Topic: topic, IsGroup: true, GroupName: groupName, Body: body}
	for _, uid := range uids {
		if tokens := targets[uid]; len(tokens) > 0 {
			h.sendPush(uid, tokens, n, "pushJoin")
		}
	}
}

// groupJoinModule is scan-to-join: QR tokens, join requests and their review.
type groupJoinModule struct{}

func (groupJoinModule) name() string { return "groupjoin" }

func (groupJoinModule) actions(h *Hub) map[string]func(*Client, *ClientMessage) {
	return map[string]func(*Client, *ClientMessage){
		ActionGroupQrToken:            h.handleGroupQrToken,
		ActionGroupQrResolve:          h.handleGroupQrResolve,
		ActionGroupJoin:               h.handleGroupJoin,
		ActionGroupJoinRequestsList:   h.handleGroupJoinRequestsList,
		ActionGroupJoinReview:         h.handleGroupJoinReview,
		ActionGetGroupJoinSettings:    h.handleGetGroupJoinSettings,
		ActionUpdateGroupJoinSettings: h.handleUpdateGroupJoinSettings,
	}
}

func (groupJoinModule) migrations() (migrate.Set, error) { return migrations.GroupJoin() }

// disbandTx runs inside the disband transaction. Same F-21 shape as
// im_group_settings: im_group_invite_tokens' and im_group_join_requests' ON
// DELETE CASCADE / lack thereof both hang off im_topics(topic), which a
// disband deliberately never deletes -- so both must be cleaned up explicitly
// or a disbanded group's QR token stays scannable and its stale join requests
// stay listable forever.
func (groupJoinModule) disbandTx(tx *sql.Tx, topic string) error {
	if _, err := tx.Exec(`DELETE FROM im_group_invite_tokens WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group (invite tokens): %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM im_group_join_requests WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group (join requests): %w", err)
	}
	return nil
}

func (groupJoinModule) disbanded(*Hub, string) {}
