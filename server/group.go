// Group management: create, invite, kick, leave, roles, owner transfer, disband, rename and listings.

package main

import (
	"strings"
	"time"
	"unicode/utf8"
)

func (h *Hub) handleCreateGrp(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeAuth, "not authenticated"))
		return
	}
	rc := CurrentRuntimeConfig()
	if strings.TrimSpace(msg.Name) == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeBadReq, "group name required"))
		return
	}
	if utf8.RuneCountInString(msg.Name) > rc.NameMaxLen {
		client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeBadReq, "群名称过长"))
		return
	}
	// 先去重再算人数：CreateGroup 内部靠 ON CONFLICT DO NOTHING 吞掉重复项，
	// 拿原始 slice 的长度做上限判断会被「同一个 uid 报 500 次」直接绕过。
	// 顺带剔掉空串与群主自己，让 len(members)+1 就是真实入群人数；也让下面
	// 那圈 AreFriends 不会对同一个人重复发起 HTTP 往返。
	seen := make(map[string]bool, len(msg.Members))
	members := make([]string, 0, len(msg.Members))
	for _, m := range msg.Members {
		if m == "" || m == client.uid || seen[m] {
			continue
		}
		seen[m] = true
		members = append(members, m)
	}
	if len(members)+1 > rc.MaxMembers {
		client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeBadReq, "群成员数量超出上限"))
		return
	}
	// M14: neither this handler nor handleInvite validated ANY relationship
	// between the caller and the members being added — only that the caller
	// was authenticated (and, for invite, already a group member). Any
	// authenticated user who merely knew another user's uid could add them
	// to a brand-new group with zero consent or relationship check. Mirrors
	// resolveP2PTopic's identical AreFriends gate — im-go has no local
	// knowledge of the contacts table, so this has to be a round trip to the
	// main server (see internal_client.go's doc comment on why).
	for _, uid := range members {
		if uid == client.uid {
			continue
		}
		memberAppUserID, err := h.resolveAppUserID(uid)
		if err != nil {
			logf("resolveAppUserID(%s) failed: %v", uid, err)
			client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeAuth, "cannot verify relationship"))
			return
		}
		friends, err := h.host.AreFriends(client.appUserID, memberAppUserID)
		if err != nil {
			logf("AreFriends check failed: %v", err)
			client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeInternal, "relationship check failed"))
			return
		}
		if !friends {
			client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeAuth, "not friends: "+uid))
			return
		}
	}
	topic, err := h.store.CreateGroup(client.uid, msg.Name, members)
	if err != nil {
		logf("Create group failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeInternal, "create failed"))
		return
	}
	client.Subscribe(topic)
	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionCreateGrp, Code: CodeCreated, Topic: topic, Name: msg.Name,
	})
	logf("[Group] Created %s by %s: %s", topic, client.uid, msg.Name)

	// Notify initial members who are currently online so their conversation
	// list picks the group up immediately instead of waiting for their next
	// unrelated refresh/reconnect. DB membership alone does not put them in
	// h.topics, which is what BroadcastToTopic actually iterates — and
	// pushOfflineRecipients skips anyone holding a live connection, so without
	// subscribing them an online member got the group's messages via neither
	// WS nor FCM. Exactly the reason syncP2PPeer exists on the 1:1 path.
	//
	// F-04：建群时不在线的成员改前什么都收不到。DB 里有成员行，但端侧要等到
	// 下一次 get_groups 才可能发现自己进了这个群；在那之前群里发的消息对他
	// 既没有实时推送（不在 h.topics 里）也进不了会话列表。留一条持久事件，
	// 下次登录由 deliverPendingGroupEvents 补投。
	//
	// The creator's other devices learn about the group the same way (task
	// 1.12).
	h.deliverGroupEvent(groupEvent{
		topic: topic, kind: ActionGrpSync, name: msg.Name, from: client.uid,
		frame:      &ServerMessage{Action: ActionGrpSync, Topic: topic, Name: msg.Name, From: client.uid},
		membership: 1, queue: true,
	}, append(members, client.uid), client)
}

func (h *Hub) handleInvite(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.User == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeBadReq, "topic and user required"))
		return
	}
	// F-03：邀请只对群聊成立。改前完全没有这个判据，而 AddGroupMember 不看
	// is_group —— 任何人只要拿到一条 1:1 会话的 canonical p2p_ 串，就能往
	// im_topic_members 里插进第三个人；那个人从此就是「成员」：能订阅实时
	// 消息、能拉走全部历史、能发言。ensureTopicMember 之后也拦不住他，因为
	// 他此刻确实有成员行。
	if !strings.HasPrefix(msg.Topic, "grp_") {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeBadReq, "only group topics can be invited into"))
		return
	}
	// Any current member may invite others (mirrors WeChat's default group
	// behavior) — this used to be owner-only.
	isMember, err := h.store.IsGroupMember(msg.Topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeNotFound, "group not found"))
		return
	}
	if !isMember {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeAuth, "only group members can invite"))
		return
	}
	// 目标已经在群里就幂等返回。
	//
	// 这里此前的注释写的是「必须在所有分支之前短路，否则会落到 AddGroupMember，
	// 那条语句是 ON CONFLICT DO UPDATE SET role = 'member'，重复邀请一位管理员
	// 会把他悄悄降级」——那个越权降级属实，但已经在 store 层根治了：
	// AddGroupMember 现在是 ON CONFLICT (topic, uid) DO NOTHING（见该函数注释），
	// 角色变更只能走 SetGroupMemberRole。所以本处短路**不再**是安全边界，
	// 只是省掉一次注定 DO NOTHING 的写库和一轮无谓的好友校验。
	//
	// 仍然要放在好友校验之前：否则「已经在同一个群里、但后来解除了好友关系」
	// 会被误判成邀请失败。
	already, err := h.store.IsGroupMember(msg.Topic, msg.User)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeInternal, "membership check failed"))
		return
	}
	if already {
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionInvite, Code: CodeOK, Topic: msg.Topic, User: msg.User})
		return
	}
	// M14: see handleCreateGrp's identical gate for the full rationale — this
	// checked that the CALLER is a group member, but nothing verified any
	// relationship between the caller and msg.User being added.
	inviteeAppUserID, err := h.resolveAppUserID(msg.User)
	if err != nil {
		logf("resolveAppUserID(%s) failed: %v", msg.User, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeAuth, "cannot verify relationship"))
		return
	}
	friends, err := h.host.AreFriends(client.appUserID, inviteeAppUserID)
	if err != nil {
		logf("AreFriends check failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeInternal, "relationship check failed"))
		return
	}
	if !friends {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeAuth, "not friends"))
		return
	}
	// 一次 GetGroupInfo 同时供人数上限判断与下面的群名使用（改前是在通知分支里
	// 又单独查了一次）。
	info, err := h.store.GetGroupInfo(msg.Topic)
	if err != nil || info == nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeNotFound, "group not found"))
		return
	}
	if info.Members+1 > CurrentRuntimeConfig().MaxMembers {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeConflict, "群成员数量已达上限"))
		return
	}
	if err := h.store.AddGroupMember(msg.Topic, msg.User); err != nil {
		logf("Invite failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeInternal, "invite failed"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionInvite, Code: CodeOK, Topic: msg.Topic, User: msg.User})
	logf("[Group] %s invited %s to %s", client.uid, msg.User, msg.Topic)

	// Notify the invited user immediately on each device they're connected
	// from. Same gap as handleCreateGrp: AddGroupMember only wrote the DB
	// row. The invitee stayed absent from h.topics (no BroadcastToTopic
	// delivery) yet present in h.clients (so pushOfflineRecipients skipped
	// them too), and the client's own handleGroupSync adds the topic to
	// knownTopics WITHOUT subscribing — which then suppresses every
	// client-side repair path until a full re-login.
	//
	// F-05：被邀请人不在线时改前什么都不留，他要等到下一次 get_groups 才
	// 可能发现自己进了群。与 handleCreateGrp 是同一个缺口。
	h.deliverGroupEvent(groupEvent{
		topic: msg.Topic, kind: ActionGrpSync, name: info.Name, from: client.uid,
		frame:      &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, Name: info.Name, From: client.uid},
		membership: 1, queue: true,
	}, []string{msg.User}, client)
}

func (h *Hub) handleKick(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.User == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeBadReq, "topic and user required"))
		return
	}
	// F-19(a)：踢自己。RemoveGroupMember 不看角色，群主踢掉自己就留下一个
	// owner_uid 指向不存在成员行的群 —— 没人能再改名/转让/解散，也没人能被
	// 提为管理员。退群应该走 leave_grp（那里有「群主必须先转让或解散」的判据）。
	if msg.User == client.uid {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeBadReq, "cannot kick yourself"))
		return
	}
	// 群主或管理员都可以踢人（改前只有群主）。
	if !h.ensureGroupManager(client, msg, ActionKick, msg.Topic) {
		return
	}
	// F-19(b)(c)：改前不看目标角色。管理员上线后，「管理员互踢」和「管理员踢
	// 群主」都会成立 —— 后者尤其致命，等于任何一个管理员都能把群主踢出他自己
	// 的群，之后这个群永远没有群主。
	targetRole, err := h.store.GetMemberRole(msg.Topic, msg.User)
	if err != nil {
		logf("[kick] GetMemberRole failed topic=%s uid=%s: %v", msg.Topic, msg.User, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeInternal, "member lookup failed"))
		return
	}
	if targetRole == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeBadReq, "target is not a group member"))
		return
	}
	if targetRole == RoleOwner {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeAuth, "cannot remove the group owner"))
		return
	}
	if targetRole == RoleAdmin {
		// 管理员之间平级，互相踢不了；只有群主能撤掉一位管理员。
		callerIsOwner, ownerErr := h.store.IsGroupOwner(msg.Topic, client.uid)
		if ownerErr != nil {
			logf("[kick] IsGroupOwner failed topic=%s uid=%s: %v", msg.Topic, client.uid, ownerErr)
			client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeInternal, "member lookup failed"))
			return
		}
		if !callerIsOwner {
			client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeAuth, "only group owner can remove an administrator"))
			return
		}
	}
	if err := h.store.RemoveGroupMember(msg.Topic, msg.User); err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeInternal, "kick failed"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionKick, Code: CodeOK, Topic: msg.Topic, User: msg.User})
	logf("[Group] %s kicked %s from %s", client.uid, msg.User, msg.Topic)

	// RemoveGroupMember only clears the DB row — without this, a kicked user
	// who's still connected keeps their in-memory topic subscription and
	// keeps receiving live group messages until their next reconnect. Reuse
	// grp_disband: from the kicked user's point of view "I'm no longer in
	// this group" has the same client-side effect (drop it from the
	// conversation list) as the group being disbanded.
	//
	// F-07：被踢的人不在线时改前什么都不留。他的成员行已经没了，
	// GetTopicMemberUIDs 从此不再枚举他，所以也不会有任何后续消息或推送
	// 顺带纠正他本地那条会话 —— 那个群会一直挂在他的会话列表里，点进去
	// 是空的、发消息 401。
	h.deliverGroupEvent(groupEvent{
		topic: msg.Topic, kind: ActionGrpDisband, from: client.uid,
		frame:      &ServerMessage{Action: ActionGrpDisband, Topic: msg.Topic, From: client.uid},
		membership: -1, queue: true,
	}, []string{msg.User}, client)
	// Nudge remaining members so an open group-info/member-list screen
	// refreshes immediately instead of showing the kicked user until reload.
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, From: client.uid}, "")

	h.mods.memberRemoved(h, msg.Topic, client.uid, "member_removed", "")
}

// leftGroupOnOtherDevices takes a group off the leaver's other devices (task
// 1.12): they get the grp_disband a kicked member gets, live or queued.
func (h *Hub) leftGroupOnOtherDevices(client *Client, topic string) {
	h.deliverGroupEvent(groupEvent{
		topic: topic, kind: ActionGrpDisband, from: client.uid,
		frame:      &ServerMessage{Action: ActionGrpDisband, Topic: topic, From: client.uid},
		membership: -1, queue: true,
	}, []string{client.UID()}, client)
}

func (h *Hub) handleLeaveGrp(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeBadReq, "topic required"))
		return
	}
	// Owners can't just walk away and leave the group ownerless — they must
	// transfer ownership or disband first, unless they're the last member
	// left, in which case leaving is equivalent to disbanding.
	// F-20：改前是 err == nil && isOwner。查询一失败就当「不是群主」，直接落到
	// 下面的 RemoveGroupMember —— 群主被当成普通成员移除，群从此无主。与紧邻的
	// GetGroupInfo 失败分支同一个判据：确认不了状态就报错让客户端重试，绝不猜。
	isOwner, ownerErr := h.store.IsGroupOwner(msg.Topic, client.uid)
	if ownerErr != nil {
		logf("[leave_grp] IsGroupOwner failed topic=%s uid=%s: %v", msg.Topic, client.uid, ownerErr)
		client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeInternal, "无法确认群组状态，请重试"))
		return
	}
	if isOwner {
		info, infoErr := h.store.GetGroupInfo(msg.Topic)
		// H8: GetGroupInfo failing is NOT the same fact as "this group has
		// one member left" — the old code only guarded the "still has other
		// members" branch on infoErr == nil, so any transient error here
		// (DB hiccup, connection blip) fell straight through to the
		// disband call below and silently destroyed a group that could
		// have had any number of other members in it. Report the failure
		// and let the client retry instead of guessing.
		if infoErr != nil {
			client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeInternal, "无法确认群组状态，请重试"))
			return
		}
		if info != nil && info.Members > 1 {
			client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeConflict, "群主需先转让群主身份或解散群聊，才能退出群聊"))
			return
		}
		// Confirmed last member — leaving really is a disband. Broadcast
		// before removing membership rows (same ordering as handleDisbandGrp,
		// and for the same reason: BroadcastToTopic only reaches clients
		// whose in-memory subscription is still intact). In the ordinary
		// case there's nobody else subscribed to hear it, but this closes
		// the gap for the case that motivated the infoErr check above: if
		// membership accounting is ever off by one, remaining members now
		// get told the group is gone instead of silently losing it.
		h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpDisband, Topic: msg.Topic, From: client.uid}, client.uid)
		if err := h.store.DisbandGroup(msg.Topic, h.mods.disbandTx()...); err != nil {
			client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeInternal, "leave failed"))
			return
		}
		client.Unsubscribe(msg.Topic)
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionLeaveGrp, Code: CodeOK, Topic: msg.Topic})
		h.leftGroupOnOtherDevices(client, msg.Topic)
		return
	}
	if err := h.store.RemoveGroupMember(msg.Topic, client.uid); err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeInternal, "leave failed"))
		return
	}
	client.Unsubscribe(msg.Topic)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionLeaveGrp, Code: CodeOK, Topic: msg.Topic})
	h.leftGroupOnOtherDevices(client, msg.Topic)
	// H9: an ordinary member leaving used to notify nobody. Remaining
	// members' in-memory subscription (h.topics[topic]) still includes
	// this client at the moment RemoveGroupMember's DB row disappears, so
	// unlike the owner-leaves-last case above there ARE other people who
	// need to hear about it — mirrors handleKick's identical nudge.
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, From: client.uid}, client.uid)
	h.mods.memberRemoved(h, msg.Topic, client.uid, "member_left", client.uid)
}

func (h *Hub) handleTransferOwner(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.User == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeBadReq, "topic and user required"))
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeNotFound, "group not found"))
		return
	}
	if !isOwner {
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeAuth, "only group owner can transfer ownership"))
		return
	}
	if isMember, err := h.store.IsGroupMember(msg.Topic, msg.User); err != nil || !isMember {
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeBadReq, "target is not a group member"))
		return
	}
	if err := h.store.TransferGroupOwnership(msg.Topic, client.uid, msg.User); err != nil {
		logf("TransferGroupOwnership failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeInternal, "transfer failed"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionTransferOwner, Code: CodeOK, Topic: msg.Topic, User: msg.User})
	logf("[Group] %s transferred ownership of %s to %s", client.uid, msg.Topic, msg.User)

	// Nudge every currently-connected member (not just the new owner) so any
	// open group-info screen refreshes its role badges immediately.
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, From: client.uid}, "")
}

// setMemberRoleAck 是 set_member_role 的成功回执。
//
// 刻意不复用 ServerMessage：协议契约要求这条回执带 memberRole 字段，而
// ServerMessage 目前没有这个字段，protocol.go 又不归本文件改。用一个只服务于
// 这一条回执的私有结构体，产出的 JSON 与契约逐字一致（id/action/code/topic/
// user/memberRole），端侧按 (action, id) 关联的方式完全不受影响。失败路径仍
// 走 ErrorResponse（那些帧本来就不需要 memberRole）。
type setMemberRoleAck struct {
	Id         string `json:"id,omitempty"`
	Action     string `json:"action"`
	Code       int    `json:"code"`
	Topic      string `json:"topic"`
	User       string `json:"user"`
	MemberRole string `json:"memberRole"`
}

// handleSetMemberRole 授予/收回管理员，**仅群主**可用（决策 D1 只放宽了清空
// 群记录，任命管理员本身仍是群主专属：管理员能自己造管理员的话，一次误操作就
// 会不可收敛地扩散下去）。
//
// RoleOwner 永远不可由此赋予：群主身份只能经 transfer_owner 移动，那条路径会
// 在同一个事务里把旧群主降级，保证任何时刻只有一行 owner。
func (h *Hub) handleSetMemberRole(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.User == "" || msg.MemberRole == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeBadReq, "topic, user and memberRole required"))
		return
	}
	if msg.MemberRole != RoleAdmin && msg.MemberRole != RoleMember {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeBadReq, "invalid member role"))
		return
	}
	// 角色只对群聊有意义。1:1 会话的 im_topic_members 也有 role 列（都是
	// 'member'），不挡住的话就能往一条私聊里写出一个「管理员」，而下游
	// CanManageGroup 只看 role —— 那个人立刻拿到对这条私聊的清空权限。
	isGroup, err := h.store.IsGroupTopic(msg.Topic)
	if err != nil {
		logf("[set_member_role] IsGroupTopic failed topic=%s: %v", msg.Topic, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeNotFound, "group not found"))
		return
	}
	if !isGroup {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeBadReq, "only group topics support member roles"))
		return
	}
	// 群主改自己 = 自我降级，会留下一个 owner_uid 指向 role='member' 那行的群，
	// 两个真相源从此打架。要放权就用 transfer_owner。
	if msg.User == client.uid {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeBadReq, "cannot change your own role"))
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		logf("[set_member_role] IsGroupOwner failed topic=%s uid=%s: %v", msg.Topic, client.uid, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeNotFound, "group not found"))
		return
	}
	if !isOwner {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeAuth, "only group owner can set member roles"))
		return
	}
	targetRole, err := h.store.GetMemberRole(msg.Topic, msg.User)
	if err != nil {
		logf("[set_member_role] GetMemberRole failed topic=%s uid=%s: %v", msg.Topic, msg.User, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeInternal, "member lookup failed"))
		return
	}
	if targetRole == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeBadReq, "target is not a group member"))
		return
	}
	if targetRole == msg.MemberRole {
		// 幂等短路：已经是这个角色，不写库也不再广播一次 grp_sync（重复广播会让
		// 每个在场客户端白刷一次成员名册）。
		client.SendAny(&setMemberRoleAck{
			Id: msg.Id, Action: ActionSetMemberRole, Code: CodeOK,
			Topic: msg.Topic, User: msg.User, MemberRole: msg.MemberRole,
		})
		return
	}
	if msg.MemberRole == RoleAdmin {
		// 管理员数量上限。没有它，群主可以把整群都设成管理员，而管理员现在拥有
		// 「清空全群聊天记录」这种不可逆权限。
		admins, err := h.store.CountGroupAdmins(msg.Topic)
		if err != nil {
			logf("[set_member_role] CountGroupAdmins failed topic=%s: %v", msg.Topic, err)
			client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeInternal, "member lookup failed"))
			return
		}
		if admins >= CurrentRuntimeConfig().MaxAdmins {
			client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeConflict, "admin limit reached"))
			return
		}
	}
	if err := h.store.SetGroupMemberRole(msg.Topic, msg.User, msg.MemberRole); err != nil {
		logf("[set_member_role] SetGroupMemberRole failed topic=%s uid=%s role=%s: %v", msg.Topic, msg.User, msg.MemberRole, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeInternal, "set member role failed"))
		return
	}
	client.SendAny(&setMemberRoleAck{
		Id: msg.Id, Action: ActionSetMemberRole, Code: CodeOK,
		Topic: msg.Topic, User: msg.User, MemberRole: msg.MemberRole,
	})
	logf("[Group] %s set %s role=%s in %s", client.uid, msg.User, msg.MemberRole, msg.Topic)

	// 广播给**所有**在场成员（含被改的人自己和发起者）：群信息页的角色徽章、
	// 以及「管理群聊」入口是否可见，都由这份名册决定。与 transfer_owner 的
	// 收尾一致，excludeUID 传空串。
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, From: client.uid}, "")
}

func (h *Hub) handleDisbandGrp(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionDisbandGrp, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionDisbandGrp, CodeBadReq, "topic required"))
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionDisbandGrp, CodeNotFound, "group not found"))
		return
	}
	if !isOwner {
		client.SendJSON(ErrorResponse(msg.Id, ActionDisbandGrp, CodeAuth, "only group owner can disband"))
		return
	}
	// F-07：先取成员名单。DisbandGroup 会把 im_topic_members 整组清掉，之后
	// 再也无从知道「解散那一刻谁在群里」，离线成员的补投递就没法入队了。
	members, memberErr := h.store.GetTopicMemberUIDs(msg.Topic)
	if memberErr != nil {
		// 不阻断解散本身：拿不到名单只是离线成员少一次主动通知，他们下次
		// get_groups 仍会自愈。
		logf("handleDisbandGrp: member lookup failed for %s: %v", msg.Topic, memberErr)
	}
	// Broadcast before removing membership rows — BroadcastToTopic only
	// reaches clients whose in-memory subscription is still intact.
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpDisband, Topic: msg.Topic, From: client.uid}, "")
	if err := h.store.DisbandGroup(msg.Topic, h.mods.disbandTx()...); err != nil {
		logf("DisbandGroup failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionDisbandGrp, CodeInternal, "disband failed"))
		return
	}
	// F-21：群没了，两份以 topic 为 key 的进程内缓存必须一起失效。AI 问答设置
	// 缓存 TTL 是 60s：不清的话这个 topic 万一被后续逻辑再次触及，读到的还是
	// 已解散那个群的知识库绑定。群名缓存同理。
	h.mods.disbanded(h, msg.Topic)
	h.invalidateGroupNameCache(msg.Topic)
	client.Unsubscribe(msg.Topic)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionDisbandGrp, Code: CodeOK, Topic: msg.Topic})
	logf("[Group] %s disbanded %s", client.uid, msg.Topic)
	// The broadcast above reached every connected device subscribed to the
	// group; devices that were away get the disband queued.
	reached := make(map[string][]string, len(members))
	for _, uid := range members {
		reached[uid] = h.onlineDevices(uid)
	}
	h.queueMissing(members, reached, func(uid string, delivered []string) {
		if err := h.store.EnqueuePendingGroupEvent(msg.Topic, uid, ActionGrpDisband, "", client.uid, delivered); err != nil {
			logf("handleDisbandGrp: queue for %s: %v", uid, err)
		}
	})
}

func (h *Hub) handleUpdateGrp(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGrp, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.Name == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGrp, CodeBadReq, "topic and name required"))
		return
	}
	// 群主或管理员都可以改群名（改前只有群主）。
	if !h.ensureGroupManager(client, msg, ActionUpdateGrp, msg.Topic) {
		return
	}
	// 群名长度改前完全没有校验：一条 update_grp 就能把任意长度的串写进
	// im_topics.name，而这个值会跟着每条群消息的 data 帧和每条离线 FCM 推送
	// 一起发出去。按 rune 计而不是 len()：中文群名在 UTF-8 下每字 3 字节。
	if utf8.RuneCountInString(msg.Name) > CurrentRuntimeConfig().NameMaxLen {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGrp, CodeBadReq, "群名称过长"))
		return
	}
	if err := h.store.UpdateGroupName(msg.Topic, msg.Name); err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGrp, CodeInternal, "rename failed"))
		return
	}
	h.invalidateGroupNameCache(msg.Topic)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionUpdateGrp, Code: CodeOK, Topic: msg.Topic, Name: msg.Name})
	// F-37：改前只有发起改名的那个人拿到了新群名，其余成员的会话列表要等到
	// 下一次 get_groups 才更新 —— 群名在他们眼里可以过期几个小时。排除发起者：
	// 他刚拿到的那条 200 ack 里已经带着新名字了。
	h.BroadcastToTopicExcept(msg.Topic, &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, Name: msg.Name, From: client.uid}, client)
}

func (h *Hub) handleGetGrps(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetGrps, CodeAuth, "not authenticated"))
		return
	}
	groups, err := h.store.GetUserGroups(client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetGrps, CodeInternal, "retrieve failed"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGetGrps, Code: CodeOK, Groups: groups})
}

func (h *Hub) handleGetMbrs(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetMbrs, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetMbrs, CodeBadReq, "topic required"))
		return
	}
	if !h.ensureTopicMember(client, msg, ActionGetMbrs, msg.Topic) {
		return
	}
	members, err := h.store.GetGroupMembers(msg.Topic)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetMbrs, CodeNotFound, "group not found"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGetMbrs, Code: CodeOK, Topic: msg.Topic, Members: members})
}

// groupNameCacheTTL 兜住「群名被本进程之外的路径改掉」这种情况（管理后台直接
// 改库等）。改名走 handleUpdateGrp 时会立即失效，不必等 TTL。
const groupNameCacheTTL = 60 * time.Second

type groupNameCacheEntry struct {
	name      string
	expiresAt time.Time
}

// groupNameCached 是 Store.GetGroupInfo 前面的读穿缓存，专供每条群消息都要跑
// 的两条路径：announceMessage（data 帧带群名，F-31）与 pushOfflineRecipients
// （离线推送标题）。GetGroupInfo 一次是两条 SQL（SELECT + COUNT），放在每条
// 群消息的必经之路上不缓存代价太大。
//
// 查不到就返回空串而不是报错：群名只是展示信息，拿不到最多是标题少一行，
// 绝不该因此中断一条消息的投递。
func (h *Hub) groupNameCached(topic string) string {
	if v, ok := h.groupNameCache.Load(topic); ok {
		entry := v.(groupNameCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.name
		}
	}
	info, err := h.store.GetGroupInfo(topic)
	if err != nil || info == nil {
		return ""
	}
	h.groupNameCache.Store(topic, groupNameCacheEntry{name: info.Name, expiresAt: time.Now().Add(groupNameCacheTTL)})
	return info.Name
}

func (h *Hub) invalidateGroupNameCache(topic string) {
	h.groupNameCache.Delete(topic)
}

// ensureGroupManager 是 ensureTopicMember 的高一级：校验请求方是该群的**群主
// 或管理员**。管理员角色上线之前，「谁能改群/踢人/清空群记录」只有 owner 一个
// 判据，群主本人不在线时整个群没有任何人能维护。
//
// DB 出错一律 fail-closed，和 ensureTopicMember 同一条纪律：授权检查在故障时
// 静默放行，比功能不可用严重得多。
func (h *Hub) ensureGroupManager(client *Client, msg *ClientMessage, action, topic string) bool {
	canManage, err := h.store.CanManageGroup(topic, client.uid)
	if err != nil {
		logf("[%s] manage check failed uid=%s topic=%s: %v", action, client.uid, topic, err)
		client.SendJSON(ErrorResponse(msg.Id, action, CodeInternal, "权限校验失败，请重试"))
		return false
	}
	if !canManage {
		client.SendJSON(ErrorResponse(msg.Id, action, CodeAuth, "只有群主或管理员可以执行此操作"))
		return false
	}
	return true
}
