// Package httpadapter reaches the host over HTTP: the endpoints, timeouts and
// one-minute internal_service token of the reference deployment. It
// implements host.Host and the host side of the calls and group-AI modules.
package httpadapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"example.com/imkit/server/host"
	"github.com/golang-jwt/jwt/v5"
)

// Client calls the main Node server's internal-only endpoints.
// im-go's own Postgres connection points at the legacy `xiaotaozi` database
// (see config.go), while tables like `contacts` live in the main server's
// database — so friendship lookups have to go over HTTP instead of SQL.
type Client struct {
	baseURL string
	secret  []byte
	client  *http.Client
	// aiQaClient uses a longer timeout than the other calls on this client
	// (friendship/profile/delete-media are all fast lookups): the group AI
	// QA endpoint runs a real knowledge-base retrieval + LLM completion on
	// roubao's side, which can legitimately take several seconds.
	aiQaClient *http.Client
	// callAnswerClient: AnswerCallSession does strictly more work than the
	// other call-session endpoints on this client's default 3s budget --
	// synchronous ARTC credential generation, an atomic tick-0 billing
	// transaction (SELECT+UPDATE+INSERT), two config reads, and (as of the
	// 2026-09-10 fix for the AnswerCallSession/forceEndCallForDisconnect
	// race) a conditional UPDATE with an explicit rows-affected check. None
	// of that is a slow operation on its own, but it's several sequential DB
	// round trips under one HTTP call, and this handler blocks the client's
	// own accept-answer flow the whole time (hub.go's handleCallAccept) --
	// a real client-side timeout here, even though roubao's side went on to
	// succeed, gets misreported to both participants as "failed" and the
	// call gets torn down for a call that was actually about to work. Not
	// shared with the other call-session endpoints (CreateCallSession does
	// far less work per request; SettleCallSession/UpgradeCallSession aren't
	// gated behind a client waiting on a 15s ARTC join timeout the way this
	// one effectively is) -- widening only where the risk was actually
	// identified.
	callAnswerClient *http.Client
}

// callAnswerHTTPTimeout gives AnswerCallSession real headroom over the
// other call-session endpoints' shared 3s budget -- see callAnswerClient's
// own field doc for why.
const callAnswerHTTPTimeout = 8 * time.Second

// aiQaHTTPTimeout bounds how long dispatchAiQaTask (hub.go) will block
// waiting for roubao's /internal/im/group-ai-qa response. Matches the "硬超时
// 10秒" budget in plan §4.3; a timeout here is reported to the caller as
// reason "timeout" so it gets the same friendly fallback message as an
// explicit {ok:false,reason:"timeout"} response body would.
const aiQaHTTPTimeout = 10 * time.Second

func New(baseURL, jwtSecret string) *Client {
	return &Client{
		baseURL:          baseURL,
		secret:           []byte(jwtSecret),
		client:           &http.Client{Timeout: 3 * time.Second},
		aiQaClient:       &http.Client{Timeout: aiQaHTTPTimeout},
		callAnswerClient: &http.Client{Timeout: callAnswerHTTPTimeout},
	}
}

// mintToken signs a short-lived internal_service JWT with the secret shared
// with the main server (see server/src/utils/jwt.ts createInternalServiceToken
// / verifyInternalServiceToken — same claim shape, same secret).
func (c *Client) mintToken() (string, error) {
	claims := jwt.MapClaims{
		"role": "internal_service",
		"exp":  time.Now().Add(1 * time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(c.secret)
}

// AreFriends checks whether two app users have an accepted contact relationship.
func (c *Client) AreFriends(appUserA, appUserB string) (bool, error) {
	token, err := c.mintToken()
	if err != nil {
		return false, fmt.Errorf("mint internal token: %w", err)
	}
	reqURL := fmt.Sprintf("%s/api/v1/internal/im/friendship?a=%s&b=%s",
		c.baseURL, url.QueryEscape(appUserA), url.QueryEscape(appUserB))
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("friendship request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("friendship request status %d", resp.StatusCode)
	}

	var body struct {
		Friends bool `json:"friends"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, err
	}
	return body.Friends, nil
}

// profileCache holds recently resolved sender display name/avatar lookups,
// keyed by "viewer|sender" (the display name can differ per viewer via their
// own contact alias for the sender). Without it, a group message with many
// offline members would hit the main server once per recipient per message.
var profileCache = struct {
	mu      sync.Mutex
	entries map[string]profileCacheEntry
}{entries: make(map[string]profileCacheEntry)}

type profileCacheEntry struct {
	displayName string
	avatarURL   string
	expiresAt   time.Time
}

const profileCacheTTL = 5 * time.Minute

// GetProfile resolves how `viewerAppUserID` should see `senderAppUserID`:
// their remark name for the sender if set, else the sender's own display
// name, plus avatar URL.
func (c *Client) GetProfile(viewerAppUserID, senderAppUserID string) (string, string, error) {
	key := viewerAppUserID + "|" + senderAppUserID
	profileCache.mu.Lock()
	if entry, ok := profileCache.entries[key]; ok && time.Now().Before(entry.expiresAt) {
		profileCache.mu.Unlock()
		return entry.displayName, entry.avatarURL, nil
	}
	profileCache.mu.Unlock()

	token, err := c.mintToken()
	if err != nil {
		return "", "", fmt.Errorf("mint internal token: %w", err)
	}
	reqURL := fmt.Sprintf("%s/api/v1/internal/im/profile?viewer=%s&sender=%s",
		c.baseURL, url.QueryEscape(viewerAppUserID), url.QueryEscape(senderAppUserID))
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("profile request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("profile request status %d", resp.StatusCode)
	}

	var body struct {
		DisplayName string `json:"displayName"`
		AvatarURL   string `json:"avatarUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", err
	}

	profileCache.mu.Lock()
	profileCache.entries[key] = profileCacheEntry{
		displayName: body.DisplayName,
		avatarURL:   body.AvatarURL,
		expiresAt:   time.Now().Add(profileCacheTTL),
	}
	profileCache.mu.Unlock()

	return body.DisplayName, body.AvatarURL, nil
}

// AskGroupAiQa synchronously calls roubao's internal group AI QA endpoint
// (POST /api/v1/internal/im/group-ai-qa) and blocks — up to aiQaHTTPTimeout —
// for the LLM-backed answer. Per the protocol contract this call always gets
// a 200 with the business result in the body ({ok:true,answer:...} or
// {ok:false,reason:...}); a non-2xx status or a transport-level failure
// (including our own client-side timeout) is a genuine technical fault and is
// reported back as reason "timeout" so the caller can show one consistent
// "小桃子正在整理资料，请稍后再问" fallback regardless of the exact cause. A
// business failure may carry the host's own wording in "message"; it comes
// back as answer, for the caller to post instead of its default notice.
//
// Callers must run this from an independent goroutine (see
// Hub.dispatchAiQaTask) — never from the hot message-broadcast path.
func (c *Client) AskGroupAiQa(groupTopic, askerImUID, question, groupOwnerAppUserID string, kbIds, docIds []string) (answer string, ok bool, reason string) {
	token, err := c.mintToken()
	if err != nil {
		return "", false, "internal_error"
	}
	if kbIds == nil {
		kbIds = []string{}
	}
	if docIds == nil {
		docIds = []string{}
	}
	payload, err := json.Marshal(map[string]interface{}{
		"groupTopic":          groupTopic,
		"askerImUid":          askerImUID,
		"question":            question,
		"groupOwnerAppUserId": groupOwnerAppUserID,
		"kbIds":               kbIds,
		"docIds":              docIds,
	})
	if err != nil {
		return "", false, "internal_error"
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/internal/im/group-ai-qa", bytes.NewReader(payload))
	if err != nil {
		return "", false, "internal_error"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.aiQaClient.Do(req)
	if err != nil {
		return "", false, "timeout"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, "internal_error"
	}

	var body struct {
		Ok     bool   `json:"ok"`
		Answer string `json:"answer"`
		Reason string `json:"reason"`
		// Message is the host's own wording for a failure (optional).
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false, "internal_error"
	}
	if !body.Ok {
		return body.Message, false, body.Reason
	}
	return body.Answer, true, ""
}

// -- IM 语音/视频通话 (IM_VOICE_VIDEO_CALL_PLAN.md §4.4) --
//
// All four calls below use c.client (the same 3-second timeout as
// AreFriends/GetProfile/FetchRuntimeConfig/DeleteMedia), NOT c.aiQaClient's
// 10-second budget -- these are DB writes + a bit of token signing on
// roubao's side, not an LLM completion, so they belong with the other fast
// lookups on this client. Every one of them runs synchronously on the
// caller's own goroutine (a client's ReadPump for the three the caller is
// waiting on a business decision from; the ring-timeout AfterFunc goroutine
// or hub.settleCallAsync's own detached goroutine for SettleCallSession) --
// see hub.go's call handlers for why blocking here is the accepted cost,
// exactly the way resolveP2PTopic already blocks a handler on AreFriends.

// CreateCallSessionResult is CreateCallSession's own response shape. Not
// asymmetric with host.UpgradeCallSessionResult/host.AnswerCallSessionResult's
// struct-vs-tuple choice by accident: this one carries exactly two pieces of
// information over the wire (Ok/Reason) that map directly onto the same
// two-value shape AskGroupAiQa already returns as a bare tuple, so a tuple
// return here matches that existing precedent rather than introducing a
// struct for two fields.
//
// POST {baseURL}/api/v1/internal/im/calls/create
//
//	request:  {"callSessionId","callerAppUserId","calleeAppUserId","callType"}
//	response: {"ok":true} or {"ok":false,"reason":"insufficient_credits"|"not_friends"|...}
//
// callSessionId is minted by im-go (RandString(32), see handleCallInvite) and
// handed to roubao to use as the ImCallSession row's primary key instead of
// letting Prisma's own @default(uuid()) fire -- this is what lets
// handleCallInvite reserve the (caller,callee) busy-slot pair in its own
// in-memory callHub BEFORE this HTTP round trip even starts, closing the
// TOCTOU window where two concurrent invites to the same free callee could
// otherwise both pass a "is anyone already ringing them" check and both
// reach here.
//
// Known accepted gap for V1: because the call is reserved into callHub
// before this request is even sent, a disconnect on either side racing this
// exact round trip can fire its own SettleCallSession(callSessionId,
// "disconnected") call over an entirely independent HTTP connection with no
// ordering guarantee relative to this one. If roubao processes that settle
// before this create, the row this call goes on to insert is left
// permanently "ringing" with nothing in im-go left that will ever call
// SettleCallSession on it again (see handleCallInvite's own post-create
// status check for where this is re-flagged, not re-solved). Narrow enough
// (both a genuine mid-invite disconnect AND an unlucky request reordering)
// that it is being accepted rather than engineered around for V1, consistent
// with this codebase's existing tolerance for similar best-effort internal
// call failure modes elsewhere (e.g. AskGroupAiQa's timeout handling).
func (c *Client) CreateCallSession(callSessionID, callerAppUserID, calleeAppUserID, callType string) (ok bool, reason string, err error) {
	token, err := c.mintToken()
	if err != nil {
		return false, "", fmt.Errorf("mint internal token: %w", err)
	}
	payload, err := json.Marshal(map[string]string{
		"callSessionId":   callSessionID,
		"callerAppUserId": callerAppUserID,
		"calleeAppUserId": calleeAppUserID,
		"callType":        callType,
	})
	if err != nil {
		return false, "", err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/internal/im/calls/create", bytes.NewReader(payload))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("create call session request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("create call session status %d", resp.StatusCode)
	}
	var body struct {
		Ok     bool   `json:"ok"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, "", err
	}
	return body.Ok, body.Reason, nil
}

// AnswerCallSession settles the "someone actually answered" transition:
// generates a fresh ARTC channel id, mints one single-parameter join
// credential per participant (never the same token for both -- each encodes
// a distinct UserId per the plan §2 token algorithm, so swapping them would
// let each party join impersonating the other), deducts the first minute
// (plan §1 decision 2), starts roubao's own per-call billing tick, and
// writes ImCallSession.status="active"/startedAt. effectiveType is the type
// to bill and persist from this moment on -- ordinarily the same as the
// invite's original CallType, but voice when the callee answered a video
// invite via ClientMessage.AcceptAsVoice (plan §7.2).
//
// POST {baseURL}/api/v1/internal/im/calls/answer
//
//	request:  {"callSessionId","effectiveType"}
//	response: {"ok":true,"channelId","callerToken","calleeToken","videoQuality","denoiseEnabled","appId"}
//	       or {"ok":false,"reason":"..."}
//
// appId: ⚠️ 2026-09-10 真机联调实测追加。反编译/文档曾判断 DingRtcAuthInfo.appId
// 可以留空（token 内部已签名绑定 appId），真机 joinChannel() 实测稳定复现
// "error 16974338: Join with invalid appId"，该假设已被证伪——appId 必须显式
// 传给客户端、再由客户端写进 DingRtcAuthInfo。appId 不是需要保密的凭证（区别于
// 签名用的 AppKey），双方共用同一个值。
//
// videoQuality/denoiseEnabled are always read fresh off roubao's current
// im_call.video_quality / im_call.intelligent_denoise_enabled config at the
// moment of answering, not any cached bootstrap value -- an operator's
// change takes effect on the very next call to be answered (plan §4.4).
func (c *Client) AnswerCallSession(callSessionID, effectiveType string) (host.AnswerCallSessionResult, error) {
	var result host.AnswerCallSessionResult
	token, err := c.mintToken()
	if err != nil {
		return result, fmt.Errorf("mint internal token: %w", err)
	}
	payload, err := json.Marshal(map[string]string{
		"callSessionId": callSessionID,
		"effectiveType": effectiveType,
	})
	if err != nil {
		return result, err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/internal/im/calls/answer", bytes.NewReader(payload))
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.callAnswerClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("answer call session request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("answer call session status %d", resp.StatusCode)
	}
	var body struct {
		Ok             bool   `json:"ok"`
		Reason         string `json:"reason"`
		ChannelId      string `json:"channelId"`
		CallerToken    string `json:"callerToken"`
		CalleeToken    string `json:"calleeToken"`
		VideoQuality   string `json:"videoQuality"`
		DenoiseEnabled bool   `json:"denoiseEnabled"`
		AppId          string `json:"appId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return result, err
	}
	result = host.AnswerCallSessionResult{
		Ok: body.Ok, Reason: body.Reason, ChannelId: body.ChannelId,
		CallerToken: body.CallerToken, CalleeToken: body.CalleeToken,
		VideoQuality: body.VideoQuality, DenoiseEnabled: body.DenoiseEnabled,
		AppId: body.AppId,
	}
	return result, nil
}

// SettleCallSession tells roubao a call reached a terminal state im-go
// itself decided (hangup/reject/cancel/ring-timeout/disconnect) so it can
// stop any billing tick and write the final status/endedAt/durationSec/
// endReason. Mirrors DeleteMedia's shape exactly: fire-and-forget from the
// caller's point of view (every call site is hub.settleCallAsync, already
// off the hot path -- the peer has already been told the call is over via
// the in-memory CAS + CallSignalMessage push before this HTTP call is even
// made, so nothing here blocks a live user on roubao's persistence
// finishing), a non-2xx or transport failure is reported as a plain error
// and logged, never retried.
//
// POST {baseURL}/api/v1/internal/im/calls/settle
//
//	request:  {"callSessionId","reason"}
//	response: {"ok":true} (body otherwise ignored, same as DeleteMedia)
//
// reason is one of "user_hangup"/"no_answer"/"rejected"/"cancelled"/
// "disconnected"/"failed" -- im-go never calls this with
// "credit_exhausted"/"max_duration_reached", both of which roubao decides
// and settles on its own initiative (its billing tick), then reports back to
// im-go via POST /api/v1/internal/im/call-broadcast instead.
func (c *Client) SettleCallSession(callSessionID, reason string) error {
	token, err := c.mintToken()
	if err != nil {
		return fmt.Errorf("mint internal token: %w", err)
	}
	payload, err := json.Marshal(map[string]string{
		"callSessionId": callSessionID,
		"reason":        reason,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/internal/im/calls/settle", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("settle call session request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("settle call session status %d", resp.StatusCode)
	}
	return nil
}

// UpgradeCallSession commits a voice->video upgrade both sides have already
// agreed to over the WS handshake (call_upgrade + call_upgrade_accept) --
// updates roubao's billing tick to the video per-minute rate and persists
// ImCallSession.type="video". toType is always CallTypeVideo today (see
// ClientMessage.ToType's doc comment); it is still a parameter rather than
// hardcoded into the request body so the wire contract does not need to
// change if a downgrade direction is ever added.
//
// POST {baseURL}/api/v1/internal/im/calls/upgrade
//
//	request:  {"callSessionId","toType"}
//	response: {"ok":true,"videoQuality":"sd"} or {"ok":false,"reason":"..."}
//
// The plan does not specify a business rejection reason for this call (an
// upgrade failing outright is expected to be a technical fault, not e.g. a
// credit check -- see handleCallUpgradeAccept's own comment on this), but
// the Ok/Reason shape is kept anyway for forward compatibility: if roubao
// ever does add one (mid-call insufficient balance for the higher video
// rate, say), no wire change is needed to surface it.
func (c *Client) UpgradeCallSession(callSessionID, toType string) (host.UpgradeCallSessionResult, error) {
	var result host.UpgradeCallSessionResult
	token, err := c.mintToken()
	if err != nil {
		return result, fmt.Errorf("mint internal token: %w", err)
	}
	payload, err := json.Marshal(map[string]string{
		"callSessionId": callSessionID,
		"toType":        toType,
	})
	if err != nil {
		return result, err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/internal/im/calls/upgrade", bytes.NewReader(payload))
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return result, fmt.Errorf("upgrade call session request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("upgrade call session status %d", resp.StatusCode)
	}
	var body struct {
		Ok           bool   `json:"ok"`
		Reason       string `json:"reason"`
		VideoQuality string `json:"videoQuality"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return result, err
	}
	result = host.UpgradeCallSessionResult{Ok: body.Ok, Reason: body.Reason, VideoQuality: body.VideoQuality}
	return result, nil
}

// DeleteMedia asks the main server to permanently remove the underlying media
// blobs (MinIO object + local disk copy) for the given file URLs. Called when a
// recall/clear leaves a blob that no remaining message references, so the
// server keeps nothing behind.
func (c *Client) DeleteMedia(urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	token, err := c.mintToken()
	if err != nil {
		return fmt.Errorf("mint internal token: %w", err)
	}
	payload, err := json.Marshal(map[string][]string{"urls": urls})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/internal/im/delete-media", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete-media request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete-media request status %d", resp.StatusCode)
	}
	return nil
}

// RuntimeConfig fetches the group-chat limits the host owns
// (GET /api/v1/internal/im/runtime-config, same internal_service JWT as
// every other call on this client). The body is returned undecoded; the
// server parses it and keeps its previous values on any failure.
func (c *Client) RuntimeConfig() (json.RawMessage, error) {
	return c.getConfig("/api/v1/internal/im/runtime-config", "runtime-config")
}

// E2eeConfig fetches the E2EE feature flags the host owns
// (GET /api/v1/internal/im/e2ee-config). See RuntimeConfig.
func (c *Client) E2eeConfig() (json.RawMessage, error) {
	return c.getConfig("/api/v1/internal/im/e2ee-config", "e2ee-config")
}

func (c *Client) getConfig(path, name string) (json.RawMessage, error) {
	token, err := c.mintToken()
	if err != nil {
		return nil, fmt.Errorf("mint internal token: %w", err)
	}
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s request status %d", name, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s read: %w", name, err)
	}
	return body, nil
}

var (
	_ host.Host         = (*Client)(nil)
	_ host.CallSessions = (*Client)(nil)
	_ host.GroupAI      = (*Client)(nil)
)
