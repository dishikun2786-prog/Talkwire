package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func runtimeConfigFrom(t *testing.T, doc string) {
	t.Helper()
	var rc RuntimeConfig
	if err := json.Unmarshal([]byte(doc), &rc); err != nil {
		t.Fatal(err)
	}
	setRuntimeConfig(rc)
}

// With nothing configured, the assistant answers to "@小桃子" and its notices
// are the strings the reference server hard-coded.
func TestGroupAIDefaultsAreTheReferenceWording(t *testing.T) {
	d := defaultGroupAIText()
	if d.Name != "小桃子" {
		t.Errorf("default name %q", d.Name)
	}
	for reason, want := range map[string]string{
		"insufficient_credits": "群主积分不足，小桃子暂时无法为本群解答问题",
		"quota_exceeded":       "本群今日的小桃子问答次数已用完，请明天再试",
		"not_configured":       "群主还没有为本群配置可用的知识库，请联系群主在群设置中勾选",
		"owner_inactive":       "小桃子暂时无法为本群解答问题",
		"rate_limited":         "问得有点快，请稍后再试",
		"timeout":              "小桃子正在整理资料，请稍后再问",
		"":                     "小桃子正在整理资料，请稍后再问",
		"something_new":        "小桃子正在整理资料，请稍后再问",
	} {
		if got := aiQaFailureMessage(d, "", reason); got != want {
			t.Errorf("reason %q: %q, want %q", reason, got, want)
		}
	}
}

func TestGroupAIMentionFollowsTheConfiguredName(t *testing.T) {
	defer setRuntimeConfig(defaultRuntimeConfig())
	mentions := func(t *testing.T, cases map[string]bool) {
		t.Helper()
		for content, want := range cases {
			if got := hasValidAiMention(content); got != want {
				t.Errorf("%q: mention %v, want %v", content, got, want)
			}
		}
	}

	t.Run("default", func(t *testing.T) {
		setRuntimeConfig(defaultRuntimeConfig())
		mentions(t, map[string]bool{
			"@小桃子 帮我查一下": true,
			"@小桃子，帮我查一下": true,
			"@小桃子@张三":    true,
			"问一下 @小桃子":   true,
			"@小桃子的粉丝群":   false,
			"小桃子 在吗":     false,
		})
	})

	t.Run("a host's name, taken literally", func(t *testing.T) {
		// "@" and spaces around the name are not part of it; "." is not a
		// wildcard.
		runtimeConfigFrom(t, `{"groupAi":{"name":"  @Bot.v2 "}}`)
		if got := CurrentRuntimeConfig().GroupAI.Name; got != "Bot.v2" {
			t.Fatalf("name %q", got)
		}
		mentions(t, map[string]bool{
			"@Bot.v2 what is this": true,
			"@Bot.v2, again":       true,
			"@Bot.v2":              true,
			"@BotXv2 what is this": false,
			"@Bot.v2s":             false,
			"@小桃子 帮我查一下":           false,
		})
	})

	t.Run("a name that is only an @ keeps the default", func(t *testing.T) {
		runtimeConfigFrom(t, `{"groupAi":{"name":" @ "}}`)
		mentions(t, map[string]bool{"@小桃子 hi": true, "@ hi": false})
	})
}

func TestGroupAINoticesFromRuntimeConfig(t *testing.T) {
	defer setRuntimeConfig(defaultRuntimeConfig())
	runtimeConfigFrom(t, `{"groupAi":{"name":"Helper","quotaExceeded":"{name} is done for today","busy":""}}`)
	text := CurrentRuntimeConfig().GroupAI
	if got := aiQaFailureMessage(text, "", "quota_exceeded"); got != "Helper is done for today" {
		t.Errorf("configured notice %q", got)
	}
	// Notices the host left out or empty keep the default wording, under the
	// configured name.
	if got := aiQaFailureMessage(text, "", "timeout"); got != "Helper正在整理资料，请稍后再问" {
		t.Errorf("default notice %q", got)
	}
	// The host's own wording for a failure wins over every notice.
	if got := aiQaFailureMessage(text, "Top up to keep asking", "insufficient_credits"); got != "Top up to keep asking" {
		t.Errorf("host wording %q", got)
	}
}

// The push title for the assistant's messages follows the name it answers
// to, unless the host words the push separately.
func TestPushAssistantFollowsTheGroupAIName(t *testing.T) {
	defer setRuntimeConfig(defaultRuntimeConfig())
	runtimeConfigFrom(t, `{"groupAi":{"name":"Helper"}}`)
	if got := CurrentRuntimeConfig().PushText.Assistant; got != "Helper" {
		t.Errorf("push assistant %q, want the group AI's name", got)
	}
	runtimeConfigFrom(t, `{"groupAi":{"name":"Helper"},"pushText":{"assistant":"Helper Bot"}}`)
	if got := CurrentRuntimeConfig().PushText.Assistant; got != "Helper Bot" {
		t.Errorf("push assistant %q, want the host's push wording", got)
	}
	runtimeConfigFrom(t, `{}`)
	if got := CurrentRuntimeConfig().PushText.Assistant; got != "小桃子" {
		t.Errorf("push assistant %q with nothing configured", got)
	}
}

// fakeGroupAI answers by the question: "quota" fails with the default
// notice, "tomorrow" fails in the host's own words, anything else is
// answered.
type fakeGroupAI struct {
	mu        sync.Mutex
	questions []string
}

func (f *fakeGroupAI) AskGroupAiQa(_, _, question, _ string, _, _ []string) (string, bool, string) {
	f.mu.Lock()
	f.questions = append(f.questions, question)
	f.mu.Unlock()
	switch {
	case strings.Contains(question, "quota"):
		return "", false, "quota_exceeded"
	case strings.Contains(question, "tomorrow"):
		return "Come back tomorrow", false, "quota_exceeded"
	default:
		return "fake answer", true, ""
	}
}

// End to end on the real server: a member asks the assistant under the name
// the host configured; the group sees it typing, then the host's answer, or
// for a failure the host's own words or else the configured notice.
func TestGroupAIBotHookEndToEnd(t *testing.T) {
	defer setRuntimeConfig(defaultRuntimeConfig())
	runtimeConfigFrom(t, `{"groupAi":{"name":"Helper","quotaExceeded":"{name} is done for today"}}`)
	ai := &fakeGroupAI{}
	ls := newLiveServer(t, liveOptions{modules: []string{"groupai"}, groupAI: ai})
	owner := ls.connect("bot-owner")
	asker := ls.connect("bot-asker")

	for i, tc := range []struct{ question, reply string }{
		{"@Helper what is this", "fake answer"},
		{"@Helper quota please", "Helper is done for today"},
		{"@Helper, see you tomorrow", "Come back tomorrow"},
	} {
		// One group per question: each group has its own cooldown.
		group, err := ls.store.CreateGroup(owner.uid, fmt.Sprintf("bot-%d", i), []string{asker.uid})
		if err != nil {
			t.Fatal(err)
		}
		if err := ls.store.UpsertGroupAiSettings(group, true, []string{"kb-1"}, nil, owner.uid); err != nil {
			t.Fatal(err)
		}
		for _, c := range []*liveConn{owner, asker} {
			if reply := c.request(frame{"action": ActionSub, "topic": group}); reply.num("code") != CodeOK {
				t.Fatalf("sub: %v", reply)
			}
		}
		if reply := asker.request(frame{"action": ActionPub, "topic": group, "content": tc.question, "contentType": "text/plain"}); reply.num("code") != CodeOK {
			t.Fatalf("pub: %v", reply)
		}
		if _, ok := owner.waitFor(func(f frame) bool { return f.str("type") == TypeAiTyping && f.str("topic") == group }, 5*time.Second); !ok {
			t.Fatalf("%q: no ai_typing", tc.question)
		}
		got, ok := owner.waitFor(func(f frame) bool {
			return f.str("action") == "data" && f.str("topic") == group && f.str("from") == AiAssistantUID
		}, 5*time.Second)
		if !ok {
			t.Fatalf("%q: no reply from the assistant", tc.question)
		}
		if got.str("content") != tc.reply || got.str("senderKind") != SenderKindAiAssistant {
			t.Errorf("%q: the assistant said %q (%s), want %q", tc.question, got.str("content"), got.str("senderKind"), tc.reply)
		}
	}
	ai.mu.Lock()
	defer ai.mu.Unlock()
	if len(ai.questions) != 3 {
		t.Errorf("the host was asked %d times: %q", len(ai.questions), ai.questions)
	}
}
