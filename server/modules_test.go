package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// noCapabilities is a host that offers no module capability at all.
type noCapabilities struct{}

func TestInstallModulesRejectsUnknownName(t *testing.T) {
	_, err := installModules(&Hub{}, noCapabilities{}, []string{"groupjoin", "chat"}, false)
	if err == nil || !strings.Contains(err.Error(), `unknown module "chat"`) {
		t.Fatalf("err = %v, want unknown module", err)
	}
}

func TestInstallModulesMissingCapability(t *testing.T) {
	// Asked for explicitly: a module the host cannot support stops startup.
	if _, err := installModules(&Hub{}, noCapabilities{}, []string{"call"}, true); err == nil {
		t.Fatal("call installed on a host that does not manage call sessions")
	}
	// The default set: skipped, and the rest still installs.
	h := &Hub{}
	m, err := installModules(h, noCapabilities{}, moduleNames(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.names(), ","); got != "groupjoin,e2ee" {
		t.Errorf("installed = %s, want groupjoin,e2ee", got)
	}
	if h.callHost != nil || h.aiHost != nil {
		t.Error("a module that was not installed left its host capability on the hub")
	}
}

func TestInstallModulesKeepsRegistryOrder(t *testing.T) {
	m, err := installModules(&Hub{}, nopHost{}, []string{"call", "groupjoin", "groupai"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.names(), ","); got != "groupjoin,groupai,call" {
		t.Errorf("installed = %s, want registry order groupjoin,groupai,call", got)
	}
}

func TestModuleActionsAreDisjoint(t *testing.T) {
	owner := map[string]string{}
	for _, d := range allModules {
		mod, err := d.build(&Hub{}, nopHost{})
		if err != nil {
			t.Fatalf("%s: %v", d.name, err)
		}
		if len(mod.actions(&Hub{})) == 0 {
			t.Errorf("%s handles no action", d.name)
		}
		for action := range mod.actions(&Hub{}) {
			if prev := owner[action]; prev != "" {
				t.Errorf("action %s claimed by both %s and %s", action, prev, d.name)
			}
			owner[action] = d.name
		}
	}
}

func TestDispatchAnswersMissingModule(t *testing.T) {
	m, err := installModules(&Hub{}, noCapabilities{}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{send: make(chan []byte, 4)}
	if !m.dispatch(c, &ClientMessage{Id: "r1", Action: ActionCallInvite}) {
		t.Fatal("a module action was reported unknown")
	}
	var reply map[string]any
	if err := json.Unmarshal(<-c.send, &reply); err != nil {
		t.Fatal(err)
	}
	if reply["code"] != float64(CodeNotEnabled) || reply["reason"] != "module not enabled: call" || reply["id"] != "r1" {
		t.Errorf("reply = %v", reply)
	}
	if m.dispatch(c, &ClientMessage{Id: "r2", Action: "no_such_action"}) {
		t.Error("an unknown action was taken by the registry")
	}
	if len(c.send) != 0 {
		t.Error("an unknown action got a reply from the registry")
	}
}

func TestNilRegistryIsInert(t *testing.T) {
	// A hub built without installModules (unit tests) must run the core
	// paths that call the hooks.
	var m *modules
	if m.dispatch(&Client{}, &ClientMessage{Action: ActionCallInvite}) {
		t.Error("nil registry dispatched")
	}
	if !m.beforeStore(nil, nil, nil, "") || !m.afterStore(nil, nil, nil, "", 0) {
		t.Error("nil registry refused a message")
	}
	if len(m.disbandTx()) != 0 {
		t.Error("nil registry returned cleanups")
	}
}
