// Offline push and push-token registration.

package main

import (
	"context"
	"errors"
	"strings"
	"sync"

	"example.com/imkit/server/push"
)

// fcmPushConcurrency is the fallback bound on how many push sends run in
// parallel per message, used only when the runtime config hasn't produced a
// usable value. The live value is group_chat.fcm_concurrency; this constant
// exists purely so make(chan struct{}, n) can never be handed a n <= 0
// (a negative capacity panics, and 0 silently serializes the whole fan-out).
const fcmPushConcurrency = 8

// pushOfflineRecipients sends a push to topic members who are not
// currently connected (BroadcastToTopic only reaches online WS clients).
// Sends fan out concurrently (bounded) instead of one-at-a-time — a big
// group previously meant a fully serial chain of HTTPS calls to FCM before
// the last offline member got notified.
//
// senderKind == SenderKindAiAssistant skips the im_users lookup entirely and
// uses the configured assistant name — the reserved AI uid is never written to
// im_users, so GetAppUserID(senderUID) would fail every time otherwise (this
// is exactly what made offline push for AI replies silently no-op before —
// see plan §9.2.1).
func (h *Hub) pushOfflineRecipients(topic, senderUID, senderKind, content, contentType string) {
	rc := CurrentRuntimeConfig()
	// 运营侧的总开关（group_chat.fcm_enabled，名字是历史遗留，管所有推送通道）。
	// FCM 出问题时以前只能改服务环境变量再重启整个 im-go，等于把所有人的实时连接
	// 一起断掉。
	if !rc.FCMEnabled {
		return
	}
	if h.pusher == nil {
		return
	}
	memberUIDs, err := h.store.GetTopicMemberUIDs(topic)
	if err != nil {
		logf("pushOfflineRecipients: GetTopicMemberUIDs failed: %v", err)
		return
	}

	// F-14：先把「真正需要推」的离线成员筛出来，再一次性批量查 token 与
	// appUserId。改前是在成员循环里逐个 GetFCMTokens + GetAppUserID，一条群
	// 消息 = 2N 次数据库往返；500 人的群里发一句话就是上千次查询，而这段代码
	// 跑在每条消息的 announceMessage 之后。
	offline := make([]string, 0, len(memberUIDs))
	for _, uid := range memberUIDs {
		if uid == senderUID || h.GetClientByUID(uid) != nil {
			continue
		}
		offline = append(offline, uid)
	}
	if len(offline) == 0 {
		return
	}

	tokensByUID, err := h.store.GetPushTokensBatch(offline)
	if err != nil {
		logf("pushOfflineRecipients: GetPushTokensBatch failed: %v", err)
		return
	}

	text := rc.PushText
	body := previewText(content, contentType, text)
	isGroup := strings.HasPrefix(topic, "grp")
	groupName := ""
	if isGroup {
		groupName = h.groupNameCached(topic)
	}

	var senderAppUserID string
	if senderKind != SenderKindAiAssistant {
		senderAppUserID, err = h.store.GetAppUserID(senderUID)
		if err != nil {
			// 改前这里直接 return —— 发件人 appUserId 只用来解析「在收件人眼里
			// 这个发件人叫什么名字」（备注名优先），查不到最多是标题显示成
			// 「用户」，不该让整条离线通知链路一个人都推不出去。
			logf("pushOfflineRecipients: GetAppUserID(sender) failed, falling back to generic name: %v", err)
			senderAppUserID = ""
		}
	}

	// 只有真要解析昵称时才批量查收件人的 appUserId。
	appUserIDs := map[string]string{}
	if senderKind != SenderKindAiAssistant && senderAppUserID != "" {
		appUserIDs, err = h.store.GetAppUserIDBatch(offline)
		if err != nil {
			logf("pushOfflineRecipients: GetAppUserIDBatch failed: %v", err)
			appUserIDs = map[string]string{}
		}
	}

	concurrency := rc.FCMConcurrency
	if concurrency <= 0 {
		concurrency = fcmPushConcurrency
	}
	// M16: token/appUserId lookups above are already batched (F-14) — this
	// loop is what's left, and GetProfile is still one HTTP round trip to the
	// main server PER RECIPIENT, run synchronously in this loop before that
	// recipient's own FCM sends are even dispatched. It can't be batched away
	// like the DB lookups were: it's genuinely per-viewer (a recipient may
	// have set a personal alias for the sender, see Contact.alias), so each
	// call legitimately needs its own request. But the N calls are
	// independent of each other, and running them one-at-a-time meant a large
	// group's last offline member's push waited on every other member's round
	// trip finishing first. Each recipient's full pipeline (profile
	// resolution + that recipient's own token sends) now runs in one
	// goroutine, bounded by the same semaphore that used to bound only the
	// FCM-send step — a recipient's own multiple device tokens are still sent
	// sequentially within its goroutine (most users have one device), which
	// keeps concurrent HTTPS requests to Google bounded by roughly
	// `concurrency` the same way the original fcmPushConcurrency comment
	// intended, just now also covering the previously-serial profile lookups.
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for _, uid := range offline {
		tokens := tokensByUID[uid]
		if len(tokens) == 0 {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(uid string, tokens []push.Token) {
			defer wg.Done()
			defer func() { <-sem }()

			senderName := text.Sender
			senderAvatar := ""
			if senderKind == SenderKindAiAssistant {
				senderName = text.Assistant
			} else if senderAppUserID != "" {
				if viewerAppUserID := appUserIDs[uid]; viewerAppUserID != "" {
					name, avatar, gerr := h.host.GetProfile(viewerAppUserID, senderAppUserID)
					if gerr != nil {
						logf("pushOfflineRecipients: GetProfile failed: %v", gerr)
					} else {
						if name != "" {
							senderName = name
						}
						senderAvatar = avatar
					}
				}
			}

			// 端侧对 1:1 会话的本地键是**对端的 uid**（实时推送在 announceMessage 里也是
			// 这么改写 payloadTopic 的），而这里原样下发的是服务端内部的 canonical
			// p2p_<hash>。点击这条通知进去的会话因此挂在一个端侧永远读不到的幽灵 key 上：
			// 会话列表里看不见、历史再重复落一份、转账记录的 topic 也是错的。
			// 对收件人来说这个会话的对端就是发送方，故填 senderUID。
			// 群聊的 grp_ 前缀两侧命名一致，保持原样。
			pushTopic := topic
			if !isGroup {
				pushTopic = senderUID
			}
			h.sendPush(uid, tokens, push.Notification{
				Topic:        pushTopic,
				IsGroup:      isGroup,
				SenderName:   senderName,
				SenderAvatar: senderAvatar,
				GroupName:    groupName,
				Body:         body,
			}, "pushOfflineRecipients")
		}(uid, tokens)
	}
	wg.Wait()
}

// sendPush delivers n to each of a user's devices and forgets the tokens the
// provider reports as gone (app uninstalled, token rotated, subscription
// expired). Tokens of a provider this server has no credentials for are
// skipped quietly: registration does not depend on configuration.
func (h *Hub) sendPush(uid string, tokens []push.Token, n push.Notification, caller string) {
	for _, t := range tokens {
		err := h.pusher.Send(context.Background(), t, n)
		switch {
		case err == nil, errors.Is(err, push.ErrNotConfigured):
		case errors.Is(err, push.ErrTokenGone):
			logf("%s: a %s token of %s is no longer valid; removing it", caller, t.Provider, uid)
			if delErr := h.store.DeletePushTokenByValue(t.Value); delErr != nil {
				logf("%s: DeletePushTokenByValue failed: %v", caller, delErr)
			}
		default:
			logf("%s: %s push to %s failed: %v", caller, t.Provider, uid, err)
		}
	}
}

// previewText is a message's text, cut to 60 characters, or the configured
// placeholder of its content type.
func previewText(content, contentType string, text PushText) string {
	if content != "" {
		runes := []rune(content)
		if len(runes) > 60 {
			return string(runes[:60]) + "..."
		}
		return content
	}
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return text.Image
	case strings.HasPrefix(contentType, "video/"):
		return text.Video
	case strings.HasPrefix(contentType, "audio/"):
		return text.Audio
	default:
		return text.File
	}
}

// handleRegToken stores a device's push token. Fire-and-forget: there is no
// reply, so a token that fails validation is only logged (without its value:
// a push token addresses one device).
func (h *Hub) handleRegToken(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Token == "" {
		return
	}
	provider, token, err := push.NormalizeToken(msg.Provider, msg.Token, h.webPushHosts)
	if err != nil {
		logf("reg_token from %s refused: %v", client.uid, err)
		return
	}
	if err := h.store.SavePushToken(client.uid, provider, token); err != nil {
		logf("SavePushToken failed: %v", err)
	}
}

// handleUnregToken is the logout-time counterpart to handleRegToken. Fire-
// and-forget like reg_token: there is nothing meaningful to tell the client
// if the delete fails (they're already logging out), and no ack is expected.
// Scoped by (uid, token) via DeletePushToken rather than deleting the token
// outright, so a logout racing a fresh login on a different account with the
// same device (SavePushToken already having reassigned the row) can't delete
// the new owner's row instead of the one actually being logged out of.
//
// The token is normalised the way registration stored it (a Web Push
// subscription is kept in a canonical JSON form); one that no longer passes
// validation is deleted as given.
func (h *Hub) handleUnregToken(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Token == "" {
		return
	}
	token := msg.Token
	if _, normalized, err := push.NormalizeToken(msg.Provider, msg.Token, h.webPushHosts); err == nil {
		token = normalized
	}
	if err := h.store.DeletePushToken(client.uid, token); err != nil {
		logf("DeletePushToken failed: %v", err)
	}
}
