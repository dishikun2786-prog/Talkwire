package main

import "encoding/json"

// -- Client -> Server --
type ClientMessage struct {
	Id     string `json:"id,omitempty"`
	Action string `json:"action"`
	Token  string `json:"token,omitempty"`
	// Device names the device a login comes from (task 1.12); see devices.go.
	Device string `json:"device,omitempty"`
	// Provider names the push service of a reg_token/unreg_token token:
	// "fcm" (the default when absent), "apns" or "webpush".
	Provider    string `json:"provider,omitempty"`
	Topic       string `json:"topic,omitempty"`
	Content     string `json:"content,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	FileURL     string `json:"fileUrl,omitempty"`
	FileName    string `json:"fileName,omitempty"`
	// ClientMsgId is the client's own localId echoed back on `pub`, letting
	// the server deduplicate a resend of a pub whose ack the client never
	// saw (e.g. connection dropped mid-round-trip after the server already
	// persisted and broadcast it) instead of creating a second message with
	// a new seq. See Store.SaveMessageFullIdempotent's doc comment. Empty
	// string (older clients that haven't shipped this yet) disables
	// deduplication entirely for that pub, matching the pre-existing behavior.
	ClientMsgId string   `json:"clientMsgId,omitempty"`
	Seq         int      `json:"seq,omitempty"`
	BeforeSeq   int      `json:"beforeSeq,omitempty"`
	AfterSeq    int      `json:"afterSeq,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	What        string   `json:"what,omitempty"`
	Name        string   `json:"name,omitempty"`
	User        string   `json:"user,omitempty"`
	Members     []string `json:"members,omitempty"`
	// Topics is the batch form of Topic, currently used only by the `pres`
	// query (action=pres). `topic:"*"` means "every peer I share a topic with".
	Topics   []string        `json:"topics,omitempty"`
	Duration int             `json:"duration,omitempty"`
	Extra    json.RawMessage `json:"extra,omitempty"`

	// MemberRole carries the target role on `set_member_role` (RoleAdmin /
	// RoleMember only — RoleOwner is never assignable this way, ownership
	// moves exclusively through transfer_owner so there can never be two
	// owner rows).
	MemberRole string `json:"memberRole,omitempty"`

	// -- Group QR + join approval (see hub.go handleGroupQr*/handleGroupJoin*) --
	// QrToken carries a scanned group invite token on group_qr_resolve/
	// group_join. RefreshQr, on group_qr_token, asks for the group's current
	// token to be invalidated and replaced rather than merely returned.
	QrToken     string `json:"qrToken,omitempty"`
	RefreshQr   bool   `json:"refreshQr,omitempty"`
	JoinMessage string `json:"joinMessage,omitempty"`
	// JoinRequestID/Approve carry the target row and verdict on
	// group_join_review. RequireApproval carries the new value on
	// update_group_join_settings.
	JoinRequestID   int64 `json:"joinRequestId,omitempty"`
	Approve         bool  `json:"approve,omitempty"`
	RequireApproval bool  `json:"requireApproval,omitempty"`
	// EventID is the im_pending_group_events row id echoed back on
	// `ack_group_event`, the exact counterpart of how msg.Id carries the
	// im_pending_clears row id on `ack_topic_cleared`. It is a separate
	// field rather than reusing Id because a group event replayed from the
	// offline queue still needs Id free for normal request correlation.
	EventID int64 `json:"eventId,omitempty"`

	// -- Batch forward + group AI QA settings (see hub.go handleForward* /
	// handle*GroupAiSettings) --
	ReqID        string   `json:"reqId,omitempty"`
	SourceTopic  string   `json:"sourceTopic,omitempty"`
	SourceMsgID  int      `json:"sourceMsgId,omitempty"`
	SourceMsgIDs []int    `json:"sourceMsgIds,omitempty"`
	TargetTopic  string   `json:"targetTopic,omitempty"`
	TargetTopics []string `json:"targetTopics,omitempty"`
	AiQaEnabled  bool     `json:"aiQaEnabled,omitempty"`
	AiKbIds      []string `json:"aiKbIds,omitempty"`
	AiDocIds     []string `json:"aiDocIds,omitempty"`

	// -- E2EE (see hub.go handleE2e*) --
	//
	// E2eKind distinguishes what's riding in Content on e2e_send/e2e_terminate:
	// "msg" (a real encrypted chat message), "sender_key" (a group
	// SenderKeyDistributionMessage), "terminate" (peer notification that the
	// sender deleted their side of a 1:1 encrypted thread — Content empty).
	// Not reusing ContentType for this: that field already carries a
	// different vocabulary ("text"/"image"/...) on the unrelated `pub`
	// action, and these two action families never share a handler.
	E2eKind string `json:"e2eKind,omitempty"`
	// E2eMsgType mirrors CiphertextMessage.getType() from libsignal
	// (WHISPER_TYPE=2 / PREKEY_TYPE=3 / SENDERKEY_TYPE=7) so the receiving
	// client knows which concrete message class to deserialize Content
	// (base64) into before handing it to SessionCipher/GroupCipher. The
	// server never inspects this value — it is opaque routing metadata over
	// ciphertext the server cannot and must not decrypt.
	E2eMsgType int `json:"e2eMsgType,omitempty"`
	// E2eBundle carries the caller's own published key material on
	// e2e_publish_bundle. A nested struct (not flattened onto ClientMessage)
	// because it's the one action with enough fields that flattening would
	// make every other action's JSON shape harder to read for no benefit.
	E2eBundle *E2ePublishBundlePayload `json:"e2eBundle,omitempty"`

	// -- IM 语音/视频通话 (see hub.go handleCall*) --
	//
	// CallType is "voice" | "video" (see the CallTypeVoice/CallTypeVideo
	// constants below), sent on call_invite as the type being requested.
	// ClientCallId is the caller's own local id for this attempt -- nothing
	// server-side deduplicates on it today; it exists purely so a client that
	// resends an invite after a dropped connection (before ever hearing back)
	// has a stable value to reconcile its own local "outgoing call" UI state
	// against, mirroring ClientMsgId's role for a resent pub. CallSessionId
	// identifies an already-created call on every action AFTER call_invite
	// (call_accept/reject/cancel/hangup/upgrade*) -- the caller only learns it
	// from call_invite's own ack (ServerMessage.CallSessionId), the callee
	// from the call_ring push (CallSignalMessage.CallSessionId).
	CallType      string `json:"callType,omitempty"`
	ClientCallId  string `json:"clientCallId,omitempty"`
	CallSessionId string `json:"callSessionId,omitempty"`
	// ToType carries call_upgrade's target type. Always CallTypeVideo today
	// -- V1 only ever upgrades voice -> video, never the reverse (closing a
	// local camera mid-video-call is a device-local toggle per plan §7.5, not
	// a call_upgrade round trip) -- but it is its own field rather than a
	// hardcoded server-side assumption, so a future downgrade direction would
	// not need a wire format change.
	ToType string `json:"toType,omitempty"`
	// AcceptAsVoice lets a callee answer a VIDEO invite as a voice-only call
	// (plan §7.2 -- WeChat's own "以语音方式接听" affordance on call_accept).
	// Meaningless (and ignored) unless the call being accepted is a video
	// call. When true, the call is settled and billed as "voice" from the
	// moment it's answered, not the type the invite was originally sent as.
	AcceptAsVoice bool `json:"acceptAsVoice,omitempty"`
}

// E2ePublishBundlePayload is the X3DH+PQXDH prekey bundle a device publishes
// so other users' clients can start an encrypted session with it without
// that device needing to be online at that moment. Every field here is
// public key material or a signature over public key material — nothing in
// this struct is secret, which is exactly what makes it safe for the server
// to store and hand out to anyone who asks (see e2e_fetch_bundle). All
// binary fields are base64-encoded because JSON has no byte-string type.
type E2ePublishBundlePayload struct {
	RegistrationId        int    `json:"registrationId"`
	IdentityKey           string `json:"identityKey"`
	SignedPreKeyId        int    `json:"signedPreKeyId"`
	SignedPreKey          string `json:"signedPreKey"`
	SignedPreKeySignature string `json:"signedPreKeySignature"`
	KyberPreKeyId         int    `json:"kyberPreKeyId"`
	KyberPreKey           string `json:"kyberPreKey"`
	KyberPreKeySignature  string `json:"kyberPreKeySignature"`
	// OneTimePreKeys replenishes the server's pool of single-use Curve25519
	// prekeys for this device. Sent as a top-up batch (typically ~100 at a
	// time) rather than one at a time — X3DH's forward-secrecy benefit over
	// the signed prekey alone only exists for the sessions that got a
	// one-time key before the pool ran dry, so the client should re-publish
	// before it does.
	OneTimePreKeys []E2eOneTimePreKey `json:"oneTimePreKeys,omitempty"`
}

type E2eOneTimePreKey struct {
	KeyId  int    `json:"keyId"`
	PreKey string `json:"preKey"`
}

// -- Server -> Client (response) --
type ServerMessage struct {
	Id     string `json:"id,omitempty"`
	Action string `json:"action"`
	Code   int    `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
	// ErrCode is the machine-readable form of an error (strict profile only):
	// clients branch on it, while Reason stays opaque text. See errCodeFor.
	ErrCode string `json:"errCode,omitempty"`
	// Protocol, Capabilities and VapidKey ride on a successful login reply
	// (strict profile only); see Hub.loginReply.
	Protocol     int      `json:"protocol,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	VapidKey     string   `json:"vapidKey,omitempty"`
	Topic        string   `json:"topic,omitempty"`
	From         string   `json:"from,omitempty"`
	Seq          int      `json:"seq,omitempty"`
	Content      string   `json:"content,omitempty"`
	ContentType  string   `json:"contentType,omitempty"`
	FileURL      string   `json:"fileUrl,omitempty"`
	FileName     string   `json:"fileName,omitempty"`
	UID          string   `json:"uid,omitempty"`
	User         string   `json:"user,omitempty"`
	// Name is the topic/group display name. Besides its original uses
	// (create_grp / update_grp / get_groups replies) it is also populated on
	// the group "data" push: a recipient whose local conversation list has
	// never seen this grp_ topic before would otherwise render the incoming
	// message under a blank title until its next getGroupTopics() refresh.
	// Empty on P2P pushes, where the title comes from the peer's profile.
	Name            string            `json:"name,omitempty"`
	Online          bool              `json:"online,omitempty"`
	What            string            `json:"what,omitempty"`
	Duration        int               `json:"duration,omitempty"`
	Extra           json.RawMessage   `json:"extra,omitempty"`
	Members         []GroupMemberInfo `json:"members,omitempty"`
	Groups          []GroupInfo       `json:"groups,omitempty"`
	Topics          []TopicInfo       `json:"topics,omitempty"`
	Messages        []StoredMessage   `json:"messages,omitempty"`
	Timestamp       int64             `json:"ts,omitempty"`
	ClearVersion    int               `json:"clearVersion,omitempty"`
	ClearBeforeSeq  int               `json:"clearBeforeSeq,omitempty"`
	DeletedMessages int               `json:"deletedMessages,omitempty"`
	DeletedMedia    int               `json:"deletedMedia,omitempty"`
	ReadCursors     []ReadCursorDto   `json:"readCursors,omitempty"`

	// SenderKind/ForwardFrom* ride along on the "data" push (ActionData) so a
	// live client can tell an AI reply apart from a user message and render a
	// "转发自" badge, without a protocol version bump. See hub.go
	// announceMessage.
	SenderKind       string `json:"senderKind,omitempty"`
	ForwardFromMsgID int    `json:"forwardFromMsgId,omitempty"`
	ForwardFromTopic string `json:"forwardFromTopic,omitempty"`

	// ClientMsgId (M5) rides along on the "data" push so a client with more
	// than one live device/session on the same account -- the one realistic
	// way its own message shows up as an incoming push at all, since
	// BroadcastToTopic excludes the sending connection itself -- can
	// reconcile it against its own optimistic local record by exact id
	// instead of the fragile content/imageUrl heuristic that used to be the
	// only option (two identical-content sends, e.g. two same-amount
	// transfers, could reconcile against each other's seq). Empty when the
	// origin write didn't have a clientMsgId (forwards, AI replies).
	ClientMsgId string `json:"clientMsgId,omitempty"`

	// PresRev is a per-uid monotonic revision stamped on every presence frame
	// (both the on/off transition broadcast and the snapshot replies). Without
	// it a client receiving two presence frames out of order has literally no
	// field to arbitrate with — which is how a reconnecting user ended up
	// latched "offline" forever. Seeded from the hub's start time so it stays
	// monotonic across an im-go restart; see Hub.nextPresRev.
	PresRev int64 `json:"presRev,omitempty"`
	// States carries the batch answer to a `pres` query.
	States []PresenceState `json:"states,omitempty"`
	// LastSeenAt is Unix epoch milliseconds, 0 when unknown.
	LastSeenAt int64 `json:"lastSeenAt,omitempty"`

	// -- Group QR + join approval (see hub.go handleGroupQr*/handleGroupJoin*) --
	QrToken   string `json:"qrToken,omitempty"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
	// MemberCount/AlreadyMember/IsFull/RequireApproval answer group_qr_resolve's
	// "should I show 加入/申请加入/已满员/已是成员" question. RequireApproval is
	// reused, unchanged, as get_group_join_settings' current-value reply.
	MemberCount     int  `json:"memberCount,omitempty"`
	AlreadyMember   bool `json:"alreadyMember,omitempty"`
	IsFull          bool `json:"isFull,omitempty"`
	RequireApproval bool `json:"requireApproval,omitempty"`
	// JoinStatus is group_join's outcome ("joined"/"pending_approval"),
	// group_join_review's verdict echo ("approved"/"rejected"), and the same
	// two review verdicts again on the group_join_reviewed push to the
	// requester.
	JoinStatus   string                `json:"joinStatus,omitempty"`
	JoinRequests []GroupJoinRequestDto `json:"joinRequests,omitempty"`

	// -- E2EE relay (see hub.go handleE2eSend/handleE2eTerminate) --
	// Mirrors ClientMessage.E2eKind/E2eMsgType — see those doc comments.
	// Content carries the base64 ciphertext/distribution-message payload;
	// From is the sender uid.
	E2eKind    string `json:"e2eKind,omitempty"`
	E2eMsgType int    `json:"e2eMsgType,omitempty"`
	// E2eGroupEncrypted mirrors a grp_ topic's im_topics.encrypted flag on
	// e2e_set_group_encrypted's ack/broadcast — always true (the flag is
	// monotonic, see handleE2eSetGroupEncrypted).
	E2eGroupEncrypted bool `json:"e2eGroupEncrypted,omitempty"`

	// -- IM 语音/视频通话 (see hub.go handleCall*) --
	//
	// These five ride on the plain request/ack ServerMessage for
	// call_invite and call_accept specifically -- both are a direct,
	// id-correlated answer to the REQUESTER's own action (not a push to
	// someone else), which is exactly how every other action in this file
	// that doesn't push to a third party already answers. Contrast
	// CallSignalMessage, which carries the same shape of information for the
	// pushes this feature sends to the OTHER party (see that struct's own
	// doc comment for why those are not ServerMessage fields instead).
	//
	// CallSessionId is call_invite's ack only. ChannelId/RtcToken/
	// VideoQuality/DenoiseEnabled are call_accept's ack only -- RtcToken
	// there is always the ANSWERING client's own single-parameter ARTC join
	// credential (never the caller's -- see AnswerCallSession's doc comment
	// for why the two tokens must never be swapped).
	CallSessionId  string `json:"callSessionId,omitempty"`
	ChannelId      string `json:"channelId,omitempty"`
	RtcToken       string `json:"rtcToken,omitempty"`
	VideoQuality   string `json:"videoQuality,omitempty"`
	DenoiseEnabled bool   `json:"denoiseEnabled,omitempty"`
	// AppId rides on call_accept's ack only, alongside RtcToken -- see
	// AnswerCallSessionResult.AppId's doc comment (2026-09-10 real-device
	// finding: DingRtcAuthInfo.appId must be explicitly set client-side,
	// contrary to the earlier "token already encodes it" assumption).
	AppId string `json:"appId,omitempty"`
}

// E2eBundleMessage answers e2e_fetch_bundle. Found=false (all other fields
// zero) means the target has never published a bundle — the caller's UI
// should say so rather than silently doing nothing, since without this the
// most likely real-world cause ("the other person has never opened a chat
// screen since installing this feature") looks identical to a bug.
type E2eBundleMessage struct {
	Type                  string `json:"type"`
	ReqId                 string `json:"reqId,omitempty"`
	User                  string `json:"user"`
	Found                 bool   `json:"found"`
	DeviceId              int    `json:"deviceId,omitempty"`
	RegistrationId        int    `json:"registrationId,omitempty"`
	IdentityKey           string `json:"identityKey,omitempty"`
	SignedPreKeyId        int    `json:"signedPreKeyId,omitempty"`
	SignedPreKey          string `json:"signedPreKey,omitempty"`
	SignedPreKeySignature string `json:"signedPreKeySignature,omitempty"`
	KyberPreKeyId         int    `json:"kyberPreKeyId,omitempty"`
	KyberPreKey           string `json:"kyberPreKey,omitempty"`
	KyberPreKeySignature  string `json:"kyberPreKeySignature,omitempty"`
	// OneTimePreKeyId/OneTimePreKey are empty/zero when this device's
	// one-time prekey pool is exhausted — X3DH still works with just the
	// signed prekey, only its forward-secrecy margin narrows to what the
	// signed prekey's own rotation period provides.
	OneTimePreKeyId int    `json:"oneTimePreKeyId,omitempty"`
	OneTimePreKey   string `json:"oneTimePreKey,omitempty"`
}

type E2ePublishBundleResult struct {
	Type   string `json:"type"`
	ReqId  string `json:"reqId,omitempty"`
	Ok     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// E2eOtpkCountMessage answers e2e_otpk_count. Count is the caller's own
// account's remaining unconsumed one-time prekeys — see ActionE2eOtpkCount's
// doc comment for why this is scoped to "my own account" only.
type E2eOtpkCountMessage struct {
	Type  string `json:"type"`
	ReqId string `json:"reqId,omitempty"`
	Count int    `json:"count"`
}

type E2eSetGroupEncryptedResult struct {
	Type   string `json:"type"`
	ReqId  string `json:"reqId,omitempty"`
	Ok     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// CallSignalMessage carries every server->client PUSH for the IM
// voice/video calling feature (plan §4.2, IM_VOICE_VIDEO_CALL_PLAN.md) that
// is not a direct id-correlated answer to the recipient's own request --
// modeled on E2eBundleMessage's shape (a single struct, one "type"
// discriminator, every field optional and populated only when that
// particular push kind needs it) rather than bolted onto ServerMessage, for
// the same reason that struct exists: these pushes carry enough call-specific
// fields that folding them into ServerMessage would leave most of
// ServerMessage's existing fields meaningless noise on every one of them,
// and vice versa.
//
// Sent via Client.SendAny, never SendJSON/ErrorResponse -- there is
// deliberately no Id/reqId field, because none of these six are an ack to
// anything the RECIPIENT themselves sent. (Contrast call_invite/call_accept,
// whose OWN acks reuse the ordinary ServerMessage{Id,Action,Code,...}
// pattern precisely because those two ARE direct answers to the caller's/
// callee's own action -- see ServerMessage's own "IM 语音/视频通话" block.)
//
// Unlike ActionRecalled/ActionE2eDeliver's "distinct verb per push
// direction" convention, Type here is set to the SAME ActionCall* constant
// used for the client-facing "action" concept it represents (call_ring,
// call_answered, call_ended, call_upgrade_request, call_upgraded,
// call_upgrade_declined) -- none of those six names are also a
// client->server action in ClientMessage.Action, so there is no id/ack
// correlation to collide with the way there would be for, say, reusing
// "del" for its own push.
type CallSignalMessage struct {
	Type          string `json:"type"`
	CallSessionId string `json:"callSessionId"`
	// CallType rides on call_ring only -- what kind of call is coming in.
	CallType string `json:"callType,omitempty"`
	// CallerName/CallerAvatar ride on call_ring only, resolved via
	// InternalAPIClient.GetProfile from the CALLEE's point of view (their own
	// remark for the caller, if they've set one) -- the same viewer-relative
	// resolution pushOfflineRecipients already uses for message
	// notifications. Left empty (client shows a generic fallback) if that
	// lookup fails; a name lookup failure must never block ringing itself.
	CallerName         string `json:"callerName,omitempty"`
	CallerAvatar       string `json:"callerAvatar,omitempty"`
	RingTimeoutSeconds int    `json:"ringTimeoutSeconds,omitempty"`
	// ChannelId/RtcToken ride on call_answered only. RtcToken is always the
	// RECIPIENT's (the caller's, since this push goes to the caller) own
	// single-parameter ARTC join credential -- see ServerMessage's own
	// "IM 语音/视频通话" block for why the caller's and callee's tokens must
	// never be swapped.
	ChannelId string `json:"channelId,omitempty"`
	RtcToken  string `json:"rtcToken,omitempty"`
	// AppId rides on call_answered only, alongside RtcToken -- see
	// ServerMessage.AppId's doc comment for why this is now required
	// (2026-09-10 real-device finding).
	AppId string `json:"appId,omitempty"`
	// VideoQuality rides on call_answered (video calls only) and
	// call_upgraded -- the sd/hd dimension+frame-rate table the client looks
	// up locally (plan §5's im_call.video_quality config; this server treats
	// it as an opaque string it neither validates nor interprets).
	VideoQuality string `json:"videoQuality,omitempty"`
	// DenoiseEnabled rides on call_answered only (plan §1 decision 8) --
	// whether this device should call startIntelligentDenoise() once it
	// joins the ARTC channel. Voice and video calls both carry it; denoising
	// acts on locally-captured audio and has nothing to do with call type.
	DenoiseEnabled bool `json:"denoiseEnabled,omitempty"`
	// ToType rides on call_upgrade_request only -- always CallTypeVideo
	// today, see ClientMessage.ToType's own doc comment for why it is still
	// its own field rather than a hardcoded assumption.
	ToType string `json:"toType,omitempty"`
	// Reason rides on call_ended only -- one of ImCallSession.endReason's
	// values (user_hangup/no_answer/rejected/cancelled/disconnected/failed/
	// credit_exhausted/max_duration_reached; the last two are pushed by
	// roubao itself via POST /api/v1/internal/im/call-broadcast, not by any
	// handler in this file). im-go does not interpret this string beyond
	// choosing which literal to send for the paths it does own -- the
	// client's job is to pick the right "对方无应答"/"对方已拒绝"/... wording
	// for it (plan §7.3/§7.8).
	Reason string `json:"reason,omitempty"`
}

// GroupJoinRequestDto is the wire shape for one pending scan-to-join
// request, shown only to a group's owner/admins via group_join_requests_list.
// RequesterUID is safe to expose here (unlike GroupMemberInfo's ban on
// account-identifying fields) because reviewing an application is itself an
// admin-only action already gated by ensureGroupManager -- this is not a
// roster a regular member can ever see.
type GroupJoinRequestDto struct {
	ID            int64  `json:"id"`
	RequesterUID  string `json:"requesterUid"`
	RequesterName string `json:"requesterName,omitempty"`
	Message       string `json:"message,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
}

// PresenceState is one entry in a `pres` snapshot reply. Presence used to be
// push-only: ProcessMessage had no `pres` case at all, so a client that missed
// a transition could never ask again and had no way back to the truth.
type PresenceState struct {
	UID        string `json:"uid"`
	Online     bool   `json:"online"`
	PresRev    int64  `json:"presRev,omitempty"`
	LastSeenAt int64  `json:"lastSeenAt,omitempty"`
}

// GroupMemberInfo is the only per-member payload the public WebSocket
// protocol ever exposes.
//
// 约束（改这个结构体前先读完）：**禁止**新增 appUserId / chatId 或任何其它能
// 定位到平台账号的字段。UID 是本服务内部的 im_users.im_user_id，除了同群
// 成员名册以外没有任何地方能把它反查成一个可加好友的账号；一旦把 appUserId
// 或 chatId 放进来，任何一个群成员都能拿到同群所有人的账号并直接发起好友请
// 求——这是产品明确不要的行为（群成员之间不得互相识别与加好友），而不是一
// 个可以"顺手补上"的字段缺口。需要展示名字用 Name（由服务端按查看者视角解析
// 后填入），不要下发原始账号标识。
type GroupMemberInfo struct {
	UID      string `json:"uid"`
	Name     string `json:"name,omitempty"`
	Role     string `json:"role,omitempty"`
	JoinedAt int64  `json:"joinedAt,omitempty"`
}

// ReadCursorDto is one member's read-cursor snapshot, sent on handleGet for
// grp_ topics when group_chat.read_receipt_enabled is on -- lets a client
// that just opened the conversation (or reconnected) build its "who has
// read message X" baseline without waiting for live "note read" broadcasts
// it may have missed while offline. Seq is that member's last_read_seq, not
// a per-message flag: a message with seq <= Seq counts as read by UID.
type ReadCursorDto struct {
	UID string `json:"uid"`
	Seq int    `json:"seq"`
}

type GroupInfo struct {
	Topic   string `json:"topic"`
	Name    string `json:"name"`
	Owner   string `json:"owner,omitempty"`
	Members int    `json:"members,omitempty"`
	// Encrypted mirrors im_topics.encrypted — the only authoritative source
	// for "is this group's E2EE currently on", reachable via get_grps (this
	// struct). Added so a client can get a REAL answer to "is this group
	// encrypted right now" without a dedicated query action: get_grps
	// already exists, is already id-correlated, and already returns every
	// group the caller is in — GroupEncryptionScreen's "forceRefresh" was a
	// no-op before this because there was no query path at all, only the
	// one-shot broadcast at the moment an owner flips the switch.
	Encrypted bool `json:"encrypted,omitempty"`
}

type TopicInfo struct {
	Topic          string `json:"topic"`
	LastSeq        int    `json:"lastSeq"`
	IsGroup        bool   `json:"isGroup"`
	ClearVersion   int    `json:"clearVersion,omitempty"`
	ClearBeforeSeq int    `json:"clearBeforeSeq,omitempty"`
}

// AdminTopicInfo is exposed only on the IM service's internal, shared-secret
// HTTP bridge. The public WebSocket protocol never exposes member lists or
// message counts to arbitrary clients.
type AdminTopicInfo struct {
	Topic           string   `json:"topic"`
	IsGroup         bool     `json:"isGroup"`
	Name            string   `json:"name,omitempty"`
	LastSeq         int      `json:"lastSeq"`
	LastContent     string   `json:"lastContent,omitempty"`
	LastContentType string   `json:"lastContentType,omitempty"`
	LastMessageAt   int64    `json:"lastMessageAt,omitempty"`
	MessageCount    int      `json:"messageCount"`
	Members         []string `json:"members"`
	ClearVersion    int      `json:"clearVersion"`
	ClearBeforeSeq  int      `json:"clearBeforeSeq"`
}

type ClearTopicResult struct {
	DeletedMessages int      `json:"deletedMessages"`
	DeletedMedia    int      `json:"deletedMedia"`
	ClearVersion    int      `json:"clearVersion"`
	ClearBeforeSeq  int      `json:"clearBeforeSeq"`
	FileURLs        []string `json:"-"`
}

type StoredMessage struct {
	Topic       string          `json:"topic"`
	Seq         int             `json:"seq"`
	From        string          `json:"from"`
	Content     string          `json:"content"`
	ContentType string          `json:"contentType"`
	FileURL     string          `json:"fileUrl,omitempty"`
	FileName    string          `json:"fileName,omitempty"`
	Timestamp   int64           `json:"ts"`
	Duration    int             `json:"duration,omitempty"`
	Extra       json.RawMessage `json:"extra,omitempty"`

	// SenderKind defaults to "user" via the im_messages column default, so
	// every row (including ones written before this feature existed) always
	// has a non-empty value here. Historical rows/older clients that never
	// heard of "ai_assistant" naturally fall back to "user" semantics.
	SenderKind       string `json:"senderKind,omitempty"`
	ForwardFromMsgID int    `json:"forwardFromMsgId,omitempty"`
	ForwardFromTopic string `json:"forwardFromTopic,omitempty"`
}

// -- Batch forward + group AI QA wire types --
//
// These use a top-level "type" discriminator (not the flat ServerMessage's
// "action") per the cross-client protocol contract: their payload shape
// (per-target result arrays, reqId correlation, settings field sets) is
// different enough from ServerMessage's single do-everything struct that
// folding them in would leave most of ServerMessage's fields unused for
// every one of these actions.

// ForwardTargetResult is one entry in ForwardBroadcastResult.Results — one
// outcome per target group, since forward_broadcast is best-effort per
// target rather than all-or-nothing (see plan §3.1).
type ForwardTargetResult struct {
	Topic  string `json:"topic"`
	Ok     bool   `json:"ok"`
	Seq    int    `json:"seq,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ForwardBundleResult struct {
	Type         string `json:"type"`
	ReqId        string `json:"reqId"`
	Ok           bool   `json:"ok"`
	InsertedSeqs []int  `json:"insertedSeqs,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type ForwardBroadcastResult struct {
	Type    string                `json:"type"`
	ReqId   string                `json:"reqId"`
	Results []ForwardTargetResult `json:"results"`
}

type GroupAiSettingsMessage struct {
	Type          string   `json:"type"`
	ReqId         string   `json:"reqId,omitempty"`
	Topic         string   `json:"topic"`
	AiQaEnabled   bool     `json:"aiQaEnabled"`
	AiKbIds       []string `json:"aiKbIds"`
	AiDocIds      []string `json:"aiDocIds"`
	AiDailyQuota  int      `json:"aiDailyQuota"`
	AiCooldownSec int      `json:"aiCooldownSec"`
}

type UpdateGroupAiSettingsResult struct {
	Type   string `json:"type"`
	ReqId  string `json:"reqId"`
	Ok     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// AiTypingMessage is a lightweight, non-persisted signal broadcast when an
// @小桃子 task starts processing, so the group sees "小桃子正在思考..." instead
// of silence for the 2-10s the LLM call can take (see plan §4.3).
type AiTypingMessage struct {
	Type  string `json:"type"`
	Topic string `json:"topic"`
}

func SuccessResponse(action string, code int) *ServerMessage {
	return &ServerMessage{Action: action, Code: code}
}

// ErrorResponse 必须回填发起该请求的 id。客户端按 (action,id) 精确关联请求与响应；
// 拿不到 id 时它只能按 action 猜，而同一个 action 常有多个请求同时在飞，猜错会把
// 一个会话的响应交给另一个会话的等待者（见 docs/IM_CROSS_TOPIC_LEAK_PLAN.md R6）。
// ErrorResponse is an error frame. Under the strict profile it also carries
// the machine-readable errCode.
func ErrorResponse(id, action string, code int, reason string) *ServerMessage {
	m := &ServerMessage{Id: id, Action: action, Code: code, Reason: reason}
	if strictProtocol {
		m.ErrCode = errCodeFor(code, reason)
	}
	return m
}

const (
	ActionLogin     = "login"
	ActionSub       = "sub"
	ActionPub       = "pub"
	ActionNote      = "note"
	ActionLeave     = "leave"
	ActionGet       = "get"
	ActionDel       = "del"
	ActionData      = "data"
	ActionPres      = "pres"
	ActionInfo      = "info"
	ActionCreateGrp = "create_grp"
	ActionInvite    = "invite"
	ActionKick      = "kick"
	ActionLeaveGrp  = "leave_grp"
	ActionUpdateGrp = "update_grp"
	ActionGetGrps   = "get_groups"
	ActionGetMbrs   = "get_members"
	ActionRegToken  = "reg_token"
	// ActionUnregToken is the client-initiated counterpart to reg_token, sent
	// on logout so the device's FCM token stops being associated with an
	// account nobody is using it for anymore (see im_fcm_tokens's migration
	// doc comment in store.go for the cross-account leak this closes).
	ActionUnregToken = "unreg_token"
	// ActionGrpSync is a server->client push sent to a user when they're added
	// to a group (at creation or via invite) while connected, so their
	// conversation list can pick the group up without waiting for their next
	// unrelated refresh/reconnect. Also reused as a generic "your group
	// membership/role changed, refresh" nudge (e.g. after an ownership
	// transfer) since the payload shape is already just topic/name/from.
	ActionGrpSync       = "grp_sync"
	ActionTransferOwner = "transfer_owner"
	// ActionSetMemberRole (client->server) promotes a member to admin or
	// demotes an admin back to member. Owner-only: an admin cannot mint more
	// admins, so the "who can manage this group" set never grows without the
	// owner's own action. RoleOwner is not assignable here — ownership moves
	// only through transfer_owner, which keeps exactly one owner row.
	ActionSetMemberRole = "set_member_role"
	ActionDisbandGrp    = "disband_grp"
	// ActionGrpDisband is a server->client push telling a (former) member
	// that the group topic no longer exists — remove it from the local
	// conversation list. Members who are offline when this fires self-heal
	// on their next getGroupTopics() refresh since their membership row is
	// already gone server-side.
	ActionGrpDisband = "grp_disband"
	// ActionDelTopic (client->server) clears an entire conversation: every
	// message in the topic is hard-deleted for everyone and its media blobs
	// removed, per the product decision that a batch clear wipes both sides
	// and leaves nothing on the server.
	ActionDelTopic = "del_topic"
	// ActionRecalled / ActionTopicCleared are server->peer pushes (distinct
	// action names, NOT "del"/"del_topic", so they never collide with the
	// requester's own del/del_topic ack correlation). The receiving client
	// removes the message / clears the conversation locally, so a recall
	// vanishes with no trace on both sides.
	ActionRecalled     = "recalled"
	ActionTopicCleared = "topic_cleared"
	// ActionAckTopicCleared (client->server) confirms a per-member
	// topic_cleared push delivered from the offline queue
	// (im_pending_clears) has been applied locally -- msg.Id is that queue
	// row's id. The realtime broadcast to an already-online peer
	// (broadcastTopicCleared's target != nil branch) never carries an Id and
	// never needs an ack.
	ActionAckTopicCleared = "ack_topic_cleared"
	// ActionAckGroupEvent (client->server) is the same idea one level up:
	// it confirms a grp_sync / grp_disband push replayed from
	// im_pending_group_events has been applied locally, so the queue row can
	// be dropped. msg.EventID is that row's id. Realtime pushes to an
	// already-online member carry no EventID and need no ack.
	ActionAckGroupEvent = "ack_group_event"

	// ActionContactSync is a server->client nudge that a friendship changed.
	// Friendship itself lives entirely in the main server's contacts table,
	// which this service cannot read; the main API pushes it here via
	// /api/v1/internal/im/contact-sync. It exists because the pre-existing
	// contact_accepted push rides the remote-control /ws/device socket, not
	// the socket an IM user actually keeps open.
	ActionContactSync    = "contact_sync"
	ContactEventAccepted = "accepted"
	ContactEventRemoved  = "removed"

	// -- Batch forward + group AI QA (client->server actions) --
	ActionForwardBundle         = "forward_bundle"
	ActionForwardBroadcast      = "forward_broadcast"
	ActionGetGroupAiSettings    = "get_group_ai_settings"
	ActionUpdateGroupAiSettings = "update_group_ai_settings"

	// -- E2EE (client->server actions; see hub.go handleE2e*) --
	//
	// Encrypted content deliberately never touches im_messages, last_seq, or
	// clear_version — it does not become part of the queryable chat history
	// the rest of this server exposes (admin read tools, GetMessages,
	// backups). ActionE2eSend relays through im_e2e_envelopes purely as a
	// bounded-TTL, delete-on-delivery relay queue for the case the recipient
	// is offline; an online recipient never touches the table at all (see
	// handleE2eSend). ActionPub/handlePub is the one place that still needs
	// to know about encryption at all, and only for grp_ topics: it rejects
	// a plain pub against a topic with im_topics.encrypted=true so a client
	// bug (or a user with an old/unencrypted build) can't leak plaintext
	// into a thread the UI is telling everyone else is secure.
	ActionE2ePublishBundle = "e2e_publish_bundle"
	ActionE2eFetchBundle   = "e2e_fetch_bundle"
	// ActionE2eOtpkCount answers "how many of MY OWN unconsumed one-time
	// prekeys does the server still have" — the caller's own account only,
	// never a peer's (there is no legitimate reason to let one account probe
	// another's remaining pool size, unlike e2e_fetch_bundle which is
	// necessarily enumerable-by-permitted-peers). Lets the client's
	// publishBundle() decide whether to top up before pushing another full
	// batch of one-time keys on every login, instead of unconditionally
	// appending a fresh batch every single call and growing
	// im_e2e_one_time_pre_keys without bound once publishing moves from
	// "once, on first manual open" to "every successful login" (see the
	// client-side E2eeManager.publishBundle doc comment for why that move
	// happened).
	ActionE2eOtpkCount = "e2e_otpk_count"
	// ActionE2eSend (client->server only) carries the sender's own
	// e2e_send request; its ack (Action still e2e_send, correlated by the
	// request's own Id like every other action->ack pair in this file)
	// answers ONLY the sender. ActionE2eDeliver is the distinct
	// server->client push name for handing an incoming encrypted payload to
	// a recipient — same "distinct verb for the push direction" convention
	// ActionRecalled/ActionTopicCleared already use to stay out of the
	// requester's own del/del_topic ack correlation (see those constants'
	// doc comment) — reusing e2e_send for both directions would let an
	// incoming push from someone else be mismatched against the sender's
	// own pending e2e_send if it ever raced one, or (more likely) be
	// silently dropped by the ack-only correlation path on the client.
	ActionE2eSend    = "e2e_send"
	ActionE2eDeliver = "e2e_deliver"
	// ActionE2eTerminate (client->server) is the entire "opt out" mechanism
	// for 1:1 encryption — see the product decision in the design doc:
	// there is no "turn encryption back off" for a thread that had it, only
	// delete-and-start-a-new-plain-conversation. ActionE2eTerminated is the
	// distinct push name told to the peer, same reasoning as
	// ActionE2eDeliver above.
	ActionE2eTerminate  = "e2e_terminate"
	ActionE2eTerminated = "e2e_terminated"
	// ActionE2eClear (client->server) is the "empty the message list, keep
	// the thread" sibling ActionE2eTerminate deliberately does not have on
	// its own -- terminate always tears down the Double Ratchet session too
	// (see its comment above), which is the right default for "I'm done
	// talking to this person" but too heavy for "I just want a clean slate
	// with someone I'm still actively messaging". handleE2eClear does none
	// of terminate's session/thread teardown: it only tells the peer "please
	// drop your local copy of this conversation's messages", both sides keep
	// their ratchet state and can carry on sending immediately after. Same
	// realtime-only, no-offline-queue characteristic as terminate and for
	// the same reason -- see handleE2eClear's own comment. ActionE2eCleared
	// is the distinct push name, same "own verb per direction" convention as
	// ActionE2eTerminated.
	ActionE2eClear   = "e2e_clear"
	ActionE2eCleared = "e2e_cleared"
	// ActionE2eGroupClear (client->server) is the group counterpart to
	// ActionE2eClear -- clears an encrypted group's message history on every
	// member's device without touching the group's Sender Key session state
	// or the group's encryption posture (that's still monotonic, see
	// ActionE2eSetGroupEncrypted below; clearing history is a different,
	// reversible-in-spirit operation from turning encryption on). Gated
	// owner+admin via ensureGroupManager, the same tier plaintext group
	// clear already uses -- deliberately not the owner-only tier
	// ActionE2eSetGroupEncrypted uses, see handleE2eGroupClear's comment for
	// why. ActionE2eGroupCleared is the distinct push name, same
	// per-direction-verb convention as ActionE2eCleared/ActionE2eTerminated.
	ActionE2eGroupClear   = "e2e_group_clear"
	ActionE2eGroupCleared = "e2e_group_cleared"
	// ActionE2eSetGroupEncrypted is owner-only (same tier as
	// update_group_ai_settings) and monotonic: it always sets
	// im_topics.encrypted to true, never back to false, for the reason in
	// ActionE2eTerminate's comment — quietly downgrading a group everyone
	// believes is encrypted would make every message sent under that belief
	// retroactively exposed.
	ActionE2eSetGroupEncrypted = "e2e_set_group_encrypted"

	// -- E2EE (server->client "type" discriminator) --
	TypeE2eBundle                  = "e2e_bundle"
	TypeE2ePublishBundleResult     = "e2e_publish_bundle_result"
	TypeE2eSetGroupEncryptedResult = "e2e_set_group_encrypted_result"
	TypeE2eOtpkCountResult         = "e2e_otpk_count_result"

	// -- Group QR + join approval (client->server actions) --
	// ActionGroupQrToken mints-or-returns a group's current scan-to-join
	// token (RefreshQr=true forces a new one, gated owner+admin inside the
	// handler; viewing the current one is open to any member).
	ActionGroupQrToken = "group_qr_token"
	// ActionGroupQrResolve previews a scanned token's group -- the one
	// group_* action open to a non-member, since resolving-before-joining is
	// the entire point.
	ActionGroupQrResolve = "group_qr_resolve"
	// ActionGroupJoin is the single join entry point for both the
	// no-approval-needed and requires-approval cases; the server decides
	// which happened and answers via ServerMessage.JoinStatus rather than
	// making the client pre-guess it from an earlier resolve reply.
	ActionGroupJoin             = "group_join"
	ActionGroupJoinRequestsList = "group_join_requests_list"
	ActionGroupJoinReview       = "group_join_review"
	ActionGetGroupJoinSettings  = "get_group_join_settings"
	// ActionUpdateGroupJoinSettings is owner-only, same tier as
	// update_group_ai_settings and for the same reason: it is a safety
	// toggle, not a moment-to-moment moderation action like kick.
	ActionUpdateGroupJoinSettings = "update_group_join_settings"

	// -- Group QR + join approval (server->client pushes) --
	// ActionGroupJoinRequestPush tells a group's managers "someone applied,
	// go refresh your join-requests list" -- deliberately carries no
	// requester details itself, same "go refresh" shape as ActionGrpSync.
	ActionGroupJoinRequestPush = "group_join_request"
	// ActionGroupJoinReviewed tells the applicant their request's outcome
	// (ServerMessage.JoinStatus = "approved"/"rejected").
	ActionGroupJoinReviewed = "group_join_reviewed"

	// -- IM 语音/视频通话 (client->server actions; see hub.go handleCall*) --
	//
	// call_invite/call_accept/call_reject/call_cancel/call_hangup/
	// call_upgrade/call_upgrade_accept/call_upgrade_reject are all plain
	// request/ack actions (Id-correlated ServerMessage reply, same as every
	// other action in this file that isn't itself a push). The six PUSH
	// action names these produce (call_ring, call_answered, call_ended,
	// call_upgrade_request, call_upgraded, call_upgrade_declined) are listed
	// separately below, right next to CallSignalMessage which carries them --
	// none of the eight here doubles as one of those six, so there is no
	// "own verb per direction" renaming needed the way ActionDel/
	// ActionRecalled or ActionE2eSend/ActionE2eDeliver require.
	ActionCallInvite        = "call_invite"
	ActionCallAccept        = "call_accept"
	ActionCallReject        = "call_reject"
	ActionCallCancel        = "call_cancel"
	ActionCallHangup        = "call_hangup"
	ActionCallUpgrade       = "call_upgrade"
	ActionCallUpgradeAccept = "call_upgrade_accept"
	ActionCallUpgradeReject = "call_upgrade_reject"

	// -- IM 语音/视频通话 (server->client pushes, carried on
	// CallSignalMessage.Type -- see that struct's own doc comment) --
	ActionCallRing            = "call_ring"
	ActionCallAnswered        = "call_answered"
	ActionCallEnded           = "call_ended"
	ActionCallUpgradeRequest  = "call_upgrade_request"
	ActionCallUpgraded        = "call_upgraded"
	ActionCallUpgradeDeclined = "call_upgrade_declined"

	// -- IM 语音/视频通话 (call-type wire vocabulary; ClientMessage.CallType/
	// ToType, CallSignalMessage.CallType/ToType, ServerMessage.CallSessionId
	// block's VideoQuality is a separate, operator-configured vocabulary --
	// see im_call.video_quality in configDefaults.ts, not one of these two) --
	CallTypeVoice = "voice"
	CallTypeVideo = "video"

	// -- Batch forward + group AI QA (server->client "type" discriminator,
	// distinct namespace from the Action* constants above — see
	// ForwardBundleResult et al.'s doc comment) --
	TypeForwardBundleResult         = "forward_bundle_result"
	TypeForwardBroadcastResult      = "forward_broadcast_result"
	TypeGroupAiSettings             = "group_ai_settings"
	TypeUpdateGroupAiSettingsResult = "update_group_ai_settings_result"
	TypeAiTyping                    = "ai_typing"

	// AiAssistantUID is the reserved from_uid for messages the group AI QA
	// feature writes on the assistant's behalf. It is never inserted into
	// im_users or im_topic_members (see plan §2.2(3)) — client rendering and
	// server-side FCM sender resolution both key off SenderKind ==
	// SenderKindAiAssistant instead of comparing against this string, so it
	// mainly exists as one documented place for the reserved value.
	AiAssistantUID = "assistant_xiaotaozi"

	SenderKindUser        = "user"
	SenderKindAiAssistant = "ai_assistant"

	// -- im_topic_members.role values --
	//
	// 这三个常量是所有 **Go 代码路径**上唯一的角色字面量来源。此前
	// 'owner'/'member' 直接写在 store.go 的七八条 SQL 里，新增 admin 这一档时
	// 任何一处漏改都不会报错，只会在运行期表现为"某个入口把管理员当普通成员"。
	// 所有新增/修改角色的 SQL 一律用参数绑定传这些常量，不要再写内联字符串。
	//
	// 一处例外，刻意保留：store.go 建表 DDL 里那句
	// `role VARCHAR(20) NOT NULL DEFAULT 'member'` 仍是字面量。它是 Postgres
	// 自己的默认值表达式、不是 Go 代码路径，且**没有任何插入语句依赖它**——
	// 四条 INSERT INTO im_topic_members 全部显式绑定 role。为它引入
	// fmt.Sprintf 插值，只会把一段自解释的 schema 文本变成拼接出来的 SQL，
	// 换不到任何一致性保障。此前这行注释写的是"全项目唯一"，与该 DDL 矛盾。
	//
	// RoleOwner 有且只有一行（im_topics.owner_uid 与该行必须一致），只能经
	// transfer_owner 移动；RoleAdmin 由群主经 set_member_role 授予/收回。
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"

	// PendingGroupEventSync/Disband are the im_pending_group_events.kind
	// values; they mirror ActionGrpSync/ActionGrpDisband but are stored as
	// their own constants so a wire-protocol rename can never silently
	// invalidate already-queued rows.
	PendingGroupEventSync    = "grp_sync"
	PendingGroupEventDisband = "grp_disband"
	// PendingGroupEventJoinRequest/JoinApproved/JoinRejected are the
	// im_pending_group_events.kind values backing the two join-flow
	// notifications' offline queue, mirroring PendingGroupEventSync's own
	// "separate constant from the wire Action so a rename can't invalidate
	// queued rows" rationale. Approved/Rejected are two kinds rather than one
	// generic "reviewed" kind carrying an outcome field, matching how
	// Sync/Disband are already two kinds rather than one with a flag.
	PendingGroupEventJoinRequest  = "grp_join_request"
	PendingGroupEventJoinApproved = "grp_join_approved"
	PendingGroupEventJoinRejected = "grp_join_rejected"

	NoteKeyPress = "kp"
	NoteRead     = "read"
	PresOnline   = "on"
	PresOffline  = "off"
	// PresSnapshot is the `what` value on a batch reply to a client-initiated
	// `pres` query, so a client can tell a pulled snapshot apart from a pushed
	// transition even though both arrive as action=pres.
	PresSnapshot = "snapshot"
	// TopicAll asks for every peer sharing any topic with the caller.
	TopicAll = "*"

	TopicMe  = "me"
	TopicFnd = "fnd"

	MsgTypeText   = "text"
	MsgTypeImage  = "image/*"
	MsgTypeVideo  = "video/*"
	MsgTypeAudio  = "audio/*"
	MsgTypeFile   = "application/octet-stream"
	MsgTypeSystem = "system"

	CodeOK          = 200
	CodeCreated     = 201
	CodeBadReq      = 400
	CodeAuth        = 401
	CodeNotFound    = 404
	CodeConflict    = 409
	CodeRateLimited = 429
	CodeInternal    = 500

	// maxMessageContentLen used to live here as a fixed 20000-rune ceiling.
	// It is deleted rather than kept: the live check is against
	// CurrentRuntimeConfig().MessageMaxLen (hub.go, on every pub), and an
	// unused const is not a compile error in Go, so a stale one would sit
	// here indefinitely waiting for someone to re-wire validation to it and
	// silently override whatever the operator configured.
)
