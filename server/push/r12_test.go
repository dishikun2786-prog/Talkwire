package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
)

func keysOf(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// R12: the app, not the platform, decides how a notification looks.
//
//   - FCM: no "notification" block, only data (the platform would otherwise
//     display it itself and bypass the app).
//   - Web Push: the body is application data the service worker reads.
//   - APNs: here the red line's wording ("data only") cannot hold. A data-only
//     APNs push is a silent background push, which iOS throttles and does not
//     deliver to an app the user has quit, so offline notifications would be
//     lost. The payload carries an alert made only of the data fields, and
//     mutable-content, so the app's notification service extension decides
//     the final look -- the intent of R12. (Raised for review: R12's wording.)
func TestR12_PushPayloadIsDataOnly(t *testing.T) {
	n := Notification{Topic: "grp_1", IsGroup: true, GroupName: "G", SenderName: "Ann", SenderAvatar: "https://x/a.png", Body: "hi"}
	data := n.Data()

	// FCM, through the real send path.
	var fcmBody []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			io.WriteString(w, `{"access_token":"at"}`)
			return
		}
		fcmBody, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	f, _ := testFCM(t, srv)
	if err := f.Send(context.Background(), "dev", n); err != nil {
		t.Fatal(err)
	}
	var fcm struct {
		Message map[string]any `json:"message"`
	}
	json.Unmarshal(fcmBody, &fcm)
	if got := keysOf(fcm.Message); !reflect.DeepEqual(got, []string{"android", "data", "token"}) {
		t.Errorf("FCM message keys %v: only token, data and android priority", got)
	}
	if fcm.Message["android"].(map[string]any)["notification"] != nil {
		t.Error("FCM android config has a notification block")
	}

	// Web Push: title, body and the data, nothing else.
	plain, err := webPushPlaintext(n)
	if err != nil {
		t.Fatal(err)
	}
	var web map[string]any
	json.Unmarshal(plain, &web)
	if got := keysOf(web); !reflect.DeepEqual(got, []string{"body", "data", "title"}) {
		t.Errorf("web push payload keys %v", got)
	}
	if web["body"] != data["preview"] || web["title"] != data["groupName"]+" - "+data["senderName"] {
		t.Errorf("web push alert %v is not made of the data fields %v", web, data)
	}

	// APNs: custom keys are exactly the data; the alert only repeats data
	// fields; mutable-content hands the final look to the app.
	raw, err := apnsPayload(n)
	if err != nil {
		t.Fatal(err)
	}
	var apns map[string]any
	json.Unmarshal(raw, &apns)
	aps := apns["aps"].(map[string]any)
	delete(apns, "aps")
	custom := map[string]string{}
	for k, v := range apns {
		custom[k], _ = v.(string)
	}
	if !reflect.DeepEqual(custom, data) {
		t.Errorf("APNs custom keys %v, want exactly the data %v", custom, data)
	}
	alert := aps["alert"].(map[string]any)
	if alert["title"] != data["groupName"] || alert["subtitle"] != data["senderName"] || alert["body"] != data["preview"] {
		t.Errorf("APNs alert %v is not made of the data fields", alert)
	}
	if aps["mutable-content"] != float64(1) {
		t.Error("APNs payload lacks mutable-content: the app could not decide how it looks")
	}
}
