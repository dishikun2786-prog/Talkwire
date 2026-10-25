package host

// The host side of the optional modules. A host that does not offer a module
// simply does not implement its interface; the module is then left out.

// CallSessions is the host side of voice and video calls: the host owns call
// records, billing and the media-service credentials.
type CallSessions interface {
	// CreateCallSession asks the host whether a call may start. The id is
	// minted by the IM server. ok=false carries the host's reason, for
	// example "insufficient_credits" or "not_friends".
	CreateCallSession(callSessionID, callerAppUserID, calleeAppUserID, callType string) (ok bool, reason string, err error)
	// AnswerCallSession records that the callee answered and returns the
	// media-service credentials, one per participant.
	AnswerCallSession(callSessionID, effectiveType string) (AnswerCallSessionResult, error)
	// SettleCallSession reports a call's terminal state, decided by the IM
	// server: "user_hangup", "no_answer", "rejected", "cancelled",
	// "disconnected" or "failed".
	SettleCallSession(callSessionID, reason string) error
	// UpgradeCallSession commits a voice-to-video upgrade both sides agreed to.
	UpgradeCallSession(callSessionID, toType string) (UpgradeCallSessionResult, error)
}

// AnswerCallSessionResult is the host's answer to AnswerCallSession. The two
// tokens differ: each encodes its own participant, so swapping them would let
// each side join as the other.
type AnswerCallSessionResult struct {
	Ok             bool
	Reason         string
	ChannelId      string
	CallerToken    string
	CalleeToken    string
	VideoQuality   string
	DenoiseEnabled bool
	// AppId is the media service's application id, the same for both
	// participants. Clients must pass it explicitly when joining.
	AppId string
}

// UpgradeCallSessionResult is the host's answer to UpgradeCallSession.
type UpgradeCallSessionResult struct {
	Ok           bool
	Reason       string
	VideoQuality string
}

// GroupAI answers questions asked in a group by mentioning the assistant.
type GroupAI interface {
	// AskGroupAiQa blocks until the host answers. reason is set when ok is
	// false; a transport failure is reported as "timeout".
	AskGroupAiQa(groupTopic, askerImUID, question, groupOwnerAppUserID string, kbIds, docIds []string) (answer string, ok bool, reason string)
}
