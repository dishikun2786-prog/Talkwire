// Protocol profiles and the strict profile's additions.

package main

import (
	"regexp"
	"strings"
)

// Protocol profiles, chosen with IMKIT_PROTOCOL_PROFILE.
//
// strict (the default) is the protocol v1 profile. legacy answers exactly
// like the reference server -- every frame matches the golden transcripts --
// and is for a project migrating from it. strict fixes the reference
// server's defects (protocol specification section 18) and adds the protocol
// v1 improvements, all of them additions an older client ignores:
//
//   - a successful login reply and a presence snapshot reply carry the
//     request's id (F1), so a client can match every reply by id;
//   - the login reply carries the protocol version, the server's capabilities
//     and, when Web Push is configured, the VAPID public key;
//   - every error frame carries errCode, a stable machine-readable code.
const (
	ProfileLegacy = "legacy"
	ProfileStrict = "strict"
)

// strictProtocol is set once at startup, before the hub serves anyone.
var strictProtocol bool

// ProtocolVersion is the version of the protocol specification (protocol-v1)
// a strict login reply advertises.
const ProtocolVersion = 1

// Capabilities a strict server advertises. Installed modules add
// "module:<name>" and configured push providers "push:<provider>".
const (
	CapErrCode = "errcode"  // error frames carry errCode
	CapReplyID = "reply-id" // login and presence snapshot replies carry the request id
	// CapMultiDevice: a login may name its device; the user stays connected
	// on every device, and only a new login of the same device replaces a
	// connection (task 1.12).
	CapMultiDevice = "multi-device"
	// CapSyncSummary: the sync_summary action (protocol specification 8.1).
	CapSyncSummary = "sync-summary"
)

// capabilities lists what this server offers, for the login reply.
func (h *Hub) capabilities() []string {
	caps := []string{CapErrCode, CapReplyID, CapMultiDevice, CapSyncSummary}
	for _, name := range h.mods.names() {
		caps = append(caps, "module:"+name)
	}
	if h.pusher != nil {
		for _, p := range h.pusher.Providers() {
			caps = append(caps, "push:"+p)
		}
	}
	return caps
}

// loginReply is the reply to a successful login.
func (h *Hub) loginReply(id, uid string) *ServerMessage {
	m := &ServerMessage{Action: ActionLogin, Code: CodeOK, UID: uid}
	if strictProtocol {
		m.Id = id
		m.Protocol = ProtocolVersion
		m.Capabilities = h.capabilities()
		m.VapidKey = h.vapidKey
	}
	return m
}

// errCodes maps the reason text of an error frame to its errCode. Reasons are
// opaque text (some are Chinese, written for display), so a client branches on
// errCode instead. TestEveryErrorReasonHasACode checks every literal reason
// the server sends against this table.
var errCodes = map[string]string{
	"not authenticated": "not_authenticated",
	"token required":    "token_required",
	"invalid token":     "invalid_token",
	"already logged in": "already_logged_in",
	"invalid device":    "invalid_device",
	"too many devices":  "too_many_devices",
	"bad request":       "bad_request",

	"topic required":                          "missing_field",
	"topic and user required":                 "missing_field",
	"topic and seq required":                  "missing_field",
	"topic and name required":                 "missing_field",
	"topic and joinRequestId required":        "missing_field",
	"topic (callee uid) required":             "missing_field",
	"topic, user and memberRole required":     "missing_field",
	"topic or topics required":                "missing_field",
	"user required":                           "missing_field",
	"group name required":                     "missing_field",
	"qrToken required":                        "missing_field",
	"callSessionId required":                  "missing_field",
	"callSessionId and toType=video required": "missing_field",

	"message too long":         "message_too_long",
	"群名称过长":                    "group_name_too_long",
	"发送太快，请稍后再试":               "rate_limited",
	"fnd not supported":        "unsupported_topic",
	"cannot publish to me/fnd": "unsupported_topic",

	"only group topics support member roles": "not_a_group",
	"only group topics can be invited into":  "not_a_group",
	"only group topics":                      "not_a_group",
	"invalid member role":                    "invalid_member_role",
	"callType must be voice or video":        "invalid_call_type",
	"cannot kick yourself":                   "cannot_target_self",
	"cannot change your own role":            "cannot_target_self",
	"cannot call yourself":                   "cannot_target_self",
	"target is not a group member":           "target_not_member",

	"not friends":                                  "not_friends",
	"cannot verify relationship":                   "relationship_check_failed",
	"cannot verify friendship":                     "relationship_check_failed",
	"not authorized":                               "not_authorized",
	"not a member of this conversation":            "not_member",
	"only group members can invite":                "not_member",
	"只有群主或管理员可以执行此操作":                              "not_group_manager",
	"仅群主可以设置":                                      "not_group_owner",
	"only group owner can transfer ownership":      "not_group_owner",
	"only group owner can set member roles":        "not_group_owner",
	"only group owner can remove an administrator": "not_group_owner",
	"only group owner can disband":                 "not_group_owner",
	"cannot remove the group owner":                "cannot_remove_owner",
	"群主需先转让群主身份或解散群聊，才能退出群聊":                       "owner_cannot_leave",
	"admin limit reached":                          "admin_limit_reached",

	"group not found":                "group_not_found",
	"群聊不存在":                          "group_not_found",
	"群成员数量已达上限":                      "group_full",
	"群成员数量超出上限":                      "group_full",
	"message not found or not yours": "message_not_found",
	"二维码不存在或已过期":                     "qr_token_invalid",
	"该申请已被处理":                        "join_request_handled",
	"请求与群聊不匹配":                       "join_request_mismatch",

	"通话不存在或已结束":  "call_not_found",
	"通话已结束":      "call_ended",
	"该通话已被处理":    "call_already_handled",
	"该通话已被接听或结束": "call_already_handled",
	"通话尚未接通":     "call_not_active",
	"已经是视频通话":    "already_video",
	"对方已离线":      "offline",
}

// errCodePrefixes are reasons that end in a variable part.
var errCodePrefixes = []struct{ prefix, code string }{
	{"not friends: ", "not_friends"},
	{"unknown action: ", "unknown_action"},
	{"invalid JSON: ", "invalid_json"},
	{"module not enabled: ", "module_not_enabled"},
}

// snakeCase matches a reason that is already a machine-readable code (the
// E2EE reasons, and those the host returns for calls).
var snakeCase = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// errCodeFor derives an error frame's errCode: the table, then a reason that
// is already snake_case, then the generic code of its status.
func errCodeFor(code int, reason string) string {
	if c, ok := errCodes[reason]; ok {
		return c
	}
	for _, p := range errCodePrefixes {
		if strings.HasPrefix(reason, p.prefix) {
			return p.code
		}
	}
	if snakeCase.MatchString(reason) {
		return reason
	}
	switch code {
	case CodeBadReq:
		return "bad_request"
	case CodeAuth:
		return "unauthorized"
	case CodeNotFound:
		return "not_found"
	case CodeConflict:
		return "conflict"
	case CodeRateLimited:
		return "rate_limited"
	case CodeNotEnabled:
		return "module_not_enabled"
	default:
		return "internal_error"
	}
}
