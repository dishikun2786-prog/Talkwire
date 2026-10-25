package harness

import (
	"encoding/json"
	"reflect"
	"testing"
)

func frames(fs ...string) []json.RawMessage {
	var out []json.RawMessage
	for _, f := range fs {
		out = append(out, json.RawMessage(f))
	}
	return out
}

func TestStripStrictAdditions(t *testing.T) {
	tr := &Transcript{Steps: []StepRecord{{Frames: map[string][]json.RawMessage{
		"a": frames(
			`{"action":"login","capabilities":["errcode"],"code":200,"id":"s1","protocol":1,"uid":"${uid:A}"}`,
			`{"action":"pres","code":200,"id":"s2","states":[],"what":"snapshot"}`,
			`{"action":"pub","code":401,"errCode":"not_authenticated","id":"s3","reason":"not authenticated"}`,
			// Not one of the F1 replies: its id is part of the reference frame.
			`{"action":"login","code":401,"id":"s4","reason":"invalid token"}`,
			`{"action":"pres","online":true,"topic":"${uid:B}","what":"on"}`,
		),
	}}}}
	removed := tr.Strip(KnownAdditions["strict"])
	want := frames(
		`{"action":"login","code":200,"uid":"${uid:A}"}`,
		`{"action":"login","code":401,"id":"s4","reason":"invalid token"}`,
		`{"action":"pres","code":200,"states":[],"what":"snapshot"}`,
		`{"action":"pres","online":true,"topic":"${uid:B}","what":"on"}`,
		`{"action":"pub","code":401,"id":"s3","reason":"not authenticated"}`,
	)
	if got := tr.Steps[0].Frames["a"]; !reflect.DeepEqual(got, want) {
		t.Errorf("frames\n got  %s\n want %s", got, want)
	}
	if wantN := map[string]int{"capabilities": 1, "protocol": 1, "errCode": 1, "id": 2}; !reflect.DeepEqual(removed, wantN) {
		t.Errorf("removed %v, want %v", removed, wantN)
	}
}
