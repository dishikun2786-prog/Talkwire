package main

import (
	"encoding/json"
	"testing"
)

// The defaults are the strings the reference server hard-coded.
func TestDefaultPushTextIsTheReferenceWording(t *testing.T) {
	d := defaultPushText()
	group := "G"
	for got, want := range map[string]string{
		previewText("", "image/png", d):       "[图片]",
		previewText("", "video/mp4", d):       "[视频]",
		previewText("", "audio/aac", d):       "[语音]",
		previewText("", "application/pdf", d): "[附件]",
		previewText("hello", "text/plain", d): "hello",
		d.Sender:                              "用户",
		d.Assistant:                           "小桃子",
		d.group(d.JoinRequest, group):         "有人申请加入「" + group + "」，点击查看详情并审核",
		d.group(d.JoinApproved, group):        "你的入群申请已通过，欢迎加入「" + group + "」",
		d.group(d.JoinRejected, group):        "你申请加入「" + group + "」被拒绝",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestPushTextFromRuntimeConfig(t *testing.T) {
	defer setRuntimeConfig(defaultRuntimeConfig())
	var rc RuntimeConfig
	if err := json.Unmarshal([]byte(`{"maxMembers":100,"pushText":{"image":"[Photo]","joinApproved":"Welcome to {group}","sender":""}}`), &rc); err != nil {
		t.Fatal(err)
	}
	setRuntimeConfig(rc)
	text := CurrentRuntimeConfig().PushText
	if got := previewText("", "image/jpeg", text); got != "[Photo]" {
		t.Errorf("image placeholder %q", got)
	}
	if got := text.group(text.JoinApproved, "Team"); got != "Welcome to Team" {
		t.Errorf("join approved %q", got)
	}
	// Entries the host left out or empty keep the defaults.
	if text.Video != "[视频]" || text.Sender != "用户" || text.JoinRejected != defaultPushText().JoinRejected {
		t.Errorf("defaults lost: %+v", text)
	}
}

func TestPreviewTextTruncates(t *testing.T) {
	long := ""
	for i := 0; i < 70; i++ {
		long += "字"
	}
	got := previewText(long, "text/plain", defaultPushText())
	if want := string([]rune(long)[:60]) + "..."; got != want {
		t.Errorf("preview %q, want 60 characters and an ellipsis", got)
	}
}
