// Optional modules and the registry that wires them into the core.

package main

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/dishikun2786-prog/Talkwire/server/host"
	"github.com/dishikun2786-prog/Talkwire/server/migrate"
)

// CodeNotEnabled answers an action of a module that is not installed.
const CodeNotEnabled = 501

// module is an optional feature: its own actions, schema, background work and
// hooks into the core. The hooks are the optional interfaces below; the
// registry calls each at a fixed point in the core, in installation order.
// Module code lives in this package (one file per module); a module that is
// not installed has no handlers, no tables and no background work.
type module interface {
	name() string
	// actions maps every action the module handles to its handler.
	actions(h *Hub) map[string]func(*Client, *ClientMessage)
}

// Optional hooks.
type (
	// migrationsProvider owns tables.
	migrationsProvider interface {
		migrations() (migrate.Set, error)
	}
	// starter runs background work (reapers, configuration refresh) once
	// the hub is running.
	starter interface {
		start(h *Hub, cfg host.Config)
	}
	// loginHook runs, in its own goroutine, after a login was confirmed and
	// the core's own offline queues were scheduled.
	loginHook interface {
		afterLogin(h *Hub, c *Client)
	}
	// connectionHook hears about each device of a uid connecting and going
	// away (not when a newer connection of the same device supersedes it).
	connectionHook interface {
		connected(h *Hub, uid, device string)
		disconnected(h *Hub, uid, device string)
	}
	// preStoreHook can refuse a pub before it is stored; it sends the
	// error reply itself and returns false.
	preStoreHook interface {
		beforeStore(h *Hub, c *Client, msg *ClientMessage, topic string) bool
	}
	// postStoreHook runs after a new message was stored and before the
	// sender's ack. Returning false withdraws the ack: the hook has
	// corrected the store and replied itself.
	postStoreHook interface {
		afterStore(h *Hub, c *Client, msg *ClientMessage, topic string, seq int) bool
	}
	// postAckHook sees every new message a user published, after the ack.
	// This is where a bot reacts to group messages.
	postAckHook interface {
		afterAck(h *Hub, c *Client, msg *ClientMessage, topic string)
	}
	// memberRemovedHook runs after a member was kicked or left a group.
	// exclude is a uid that must not be told (the leaver), or "".
	memberRemovedHook interface {
		memberRemoved(h *Hub, topic, by, reason, exclude string)
	}
	// disbandHook cleans up after a group: disbandTx inside the disband
	// transaction, disbanded after it committed.
	disbandHook interface {
		disbandTx(tx *sql.Tx, topic string) error
		disbanded(h *Hub, topic string)
	}
	// hostBroadcastHook sees each payload the host relays to a user through
	// the internal broadcast endpoint, by its "type".
	hostBroadcastHook interface {
		hostBroadcast(h *Hub, uid, payloadType string)
	}
)

// moduleDef builds a module from the host. A module that needs a host
// capability the host lacks cannot be installed.
type moduleDef struct {
	name  string
	build func(h *Hub, hostImpl any) (module, error)
}

// allModules is every module, in installation order.
var allModules = []moduleDef{
	{"groupjoin", func(*Hub, any) (module, error) { return groupJoinModule{}, nil }},
	{"e2ee", func(*Hub, any) (module, error) { return e2eeModule{}, nil }},
	{"groupai", func(h *Hub, hostImpl any) (module, error) {
		ai, ok := hostImpl.(host.GroupAI)
		if !ok {
			return nil, fmt.Errorf("the host does not answer group AI questions")
		}
		h.aiHost = ai
		return groupAiModule{}, nil
	}},
	{"call", func(h *Hub, hostImpl any) (module, error) {
		sessions, ok := hostImpl.(host.CallSessions)
		if !ok {
			return nil, fmt.Errorf("the host does not manage call sessions")
		}
		h.callHost = sessions
		return callModule{}, nil
	}},
}

// moduleNames are the names TALKWIRE_MODULES accepts.
func moduleNames() []string {
	names := make([]string, len(allModules))
	for i, d := range allModules {
		names[i] = d.name
	}
	return names
}

// modules is the registry of installed modules.
type modules struct {
	installed []module
	handlers  map[string]func(*Client, *ClientMessage)
	// owner names the module of every module action, installed or not, so
	// an action of a missing module is answered "module not enabled" rather
	// than "unknown action".
	owner map[string]string
}

// installModules builds the named modules (in allModules order) and attaches
// them to the hub. With required set, a module the host cannot support is an
// error; otherwise it is skipped with a log line.
func installModules(h *Hub, hostImpl any, names []string, required bool) (*modules, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	m := &modules{handlers: map[string]func(*Client, *ClientMessage){}, owner: map[string]string{}}
	for _, d := range allModules {
		probe, err := d.build(&Hub{}, nopHost{})
		if err == nil {
			for action := range probe.actions(h) {
				m.owner[action] = d.name
			}
		}
		if !want[d.name] {
			continue
		}
		delete(want, d.name)
		mod, err := d.build(h, hostImpl)
		if err != nil {
			if required {
				return nil, fmt.Errorf("module %s: %w", d.name, err)
			}
			logf("module %s not installed: %v", d.name, err)
			continue
		}
		for action, fn := range mod.actions(h) {
			m.handlers[action] = fn
			m.owner[action] = d.name
		}
		m.installed = append(m.installed, mod)
	}
	for n := range want {
		return nil, fmt.Errorf("unknown module %q (known: %s)", n, strings.Join(moduleNames(), ", "))
	}
	h.mods = m
	return m, nil
}

// nopHost offers every module capability, so the action table of any module
// can be read without installing it.
type nopHost struct{}

var (
	_ host.CallSessions = nopHost{}
	_ host.GroupAI      = nopHost{}
)

func (nopHost) CreateCallSession(string, string, string, string) (bool, string, error) {
	return false, "", nil
}
func (nopHost) AnswerCallSession(string, string) (host.AnswerCallSessionResult, error) {
	return host.AnswerCallSessionResult{}, nil
}
func (nopHost) SettleCallSession(string, string) error { return nil }
func (nopHost) UpgradeCallSession(string, string) (host.UpgradeCallSessionResult, error) {
	return host.UpgradeCallSessionResult{}, nil
}
func (nopHost) AskGroupAiQa(string, string, string, string, []string, []string) (string, bool, string) {
	return "", false, ""
}

// names lists the installed modules.
func (m *modules) names() []string {
	var out []string
	if m == nil {
		return out
	}
	for _, mod := range m.installed {
		out = append(out, mod.name())
	}
	return out
}

// has reports whether a module is installed.
func (m *modules) has(name string) bool {
	if m == nil {
		return false
	}
	for _, mod := range m.installed {
		if mod.name() == name {
			return true
		}
	}
	return false
}

// dispatch handles an action no core handler took. It reports false for an
// action nobody knows.
func (m *modules) dispatch(c *Client, msg *ClientMessage) bool {
	if m == nil {
		return false
	}
	if fn := m.handlers[msg.Action]; fn != nil {
		fn(c, msg)
		return true
	}
	if name := m.owner[msg.Action]; name != "" {
		c.SendJSON(ErrorResponse(msg.Id, msg.Action, CodeNotEnabled, "module not enabled: "+name))
		return true
	}
	return false
}

func (m *modules) migrationSets() ([]migrate.Set, error) {
	var sets []migrate.Set
	if m == nil {
		return sets, nil
	}
	for _, mod := range m.installed {
		if p, ok := mod.(migrationsProvider); ok {
			set, err := p.migrations()
			if err != nil {
				return nil, err
			}
			sets = append(sets, set)
		}
	}
	return sets, nil
}

func (m *modules) start(h *Hub, cfg host.Config) {
	if m == nil {
		return
	}
	for _, mod := range m.installed {
		if s, ok := mod.(starter); ok {
			s.start(h, cfg)
		}
	}
}

func (m *modules) afterLogin(h *Hub, c *Client) {
	if m == nil {
		return
	}
	for _, mod := range m.installed {
		if hook, ok := mod.(loginHook); ok {
			go hook.afterLogin(h, c)
		}
	}
}

func (m *modules) connected(h *Hub, uid, device string) {
	if m == nil {
		return
	}
	for _, mod := range m.installed {
		if hook, ok := mod.(connectionHook); ok {
			hook.connected(h, uid, device)
		}
	}
}

func (m *modules) disconnected(h *Hub, uid, device string) {
	if m == nil {
		return
	}
	for _, mod := range m.installed {
		if hook, ok := mod.(connectionHook); ok {
			hook.disconnected(h, uid, device)
		}
	}
}

func (m *modules) beforeStore(h *Hub, c *Client, msg *ClientMessage, topic string) bool {
	if m == nil {
		return true
	}
	for _, mod := range m.installed {
		if hook, ok := mod.(preStoreHook); ok && !hook.beforeStore(h, c, msg, topic) {
			return false
		}
	}
	return true
}

func (m *modules) afterStore(h *Hub, c *Client, msg *ClientMessage, topic string, seq int) bool {
	if m == nil {
		return true
	}
	for _, mod := range m.installed {
		if hook, ok := mod.(postStoreHook); ok && !hook.afterStore(h, c, msg, topic, seq) {
			return false
		}
	}
	return true
}

func (m *modules) afterAck(h *Hub, c *Client, msg *ClientMessage, topic string) {
	if m == nil {
		return
	}
	for _, mod := range m.installed {
		if hook, ok := mod.(postAckHook); ok {
			hook.afterAck(h, c, msg, topic)
		}
	}
}

func (m *modules) memberRemoved(h *Hub, topic, by, reason, exclude string) {
	if m == nil {
		return
	}
	for _, mod := range m.installed {
		if hook, ok := mod.(memberRemovedHook); ok {
			hook.memberRemoved(h, topic, by, reason, exclude)
		}
	}
}

// disbandTx returns the cleanups that run inside the disband transaction.
func (m *modules) disbandTx() []func(*sql.Tx, string) error {
	var out []func(*sql.Tx, string) error
	if m == nil {
		return out
	}
	for _, mod := range m.installed {
		if hook, ok := mod.(disbandHook); ok {
			out = append(out, hook.disbandTx)
		}
	}
	return out
}

func (m *modules) disbanded(h *Hub, topic string) {
	if m == nil {
		return
	}
	for _, mod := range m.installed {
		if hook, ok := mod.(disbandHook); ok {
			hook.disbanded(h, topic)
		}
	}
}

func (m *modules) hostBroadcast(h *Hub, uid, payloadType string) {
	if m == nil {
		return
	}
	for _, mod := range m.installed {
		if hook, ok := mod.(hostBroadcastHook); ok {
			hook.hostBroadcast(h, uid, payloadType)
		}
	}
}
