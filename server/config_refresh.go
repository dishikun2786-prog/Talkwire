// Keeping the host's configuration documents applied.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"example.com/imkit/server/host"
)

// StartRuntimeConfigRefresh fetches the runtime config once synchronously,
// then keeps it fresh on a ticker. Launch with `go` from main.go.
//
// The first fetch is inside this goroutine rather than blocking startup on
// purpose: the defaults are already a working configuration, so an
// unreachable main server should delay config accuracy, not the IM service
// coming up. Failures are logged and the previous snapshot kept.
func StartRuntimeConfigRefresh(src host.Config, interval time.Duration) {
	startConfigRefresh(src.RuntimeConfig, "runtime-config", interval, func(raw json.RawMessage) error {
		var rc RuntimeConfig
		if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&rc); err != nil {
			return fmt.Errorf("runtime-config decode: %w", err)
		}
		setRuntimeConfig(rc)
		return nil
	})
}

// StartE2eeConfigRefresh mirrors StartRuntimeConfigRefresh exactly — see its
// doc comment for the startup-ordering and failure-handling rationale, both
// identical here.
func StartE2eeConfigRefresh(src host.Config, interval time.Duration) {
	startConfigRefresh(src.E2eeConfig, "e2ee-config", interval, func(raw json.RawMessage) error {
		var rc E2eeRuntimeConfig
		if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&rc); err != nil {
			return fmt.Errorf("e2ee-config decode: %w", err)
		}
		setE2eeRuntimeConfig(rc)
		return nil
	})
}

// startConfigRefresh applies one configuration document now and then on
// every tick. A failed fetch or decode keeps the previous values: a host
// restart or a momentary network blip must not reset every limit, and it must
// certainly not leave the server running on zeros. A host that has no such
// document (host.ErrNoConfig) is asked once.
func startConfigRefresh(fetch func() (json.RawMessage, error), name string, interval time.Duration, apply func(json.RawMessage) error) {
	refresh := func() bool {
		raw, err := fetch()
		if errors.Is(err, host.ErrNoConfig) {
			logf("%s: none provided, using built-in defaults", name)
			return false
		}
		if err == nil {
			err = apply(raw)
		}
		if err != nil {
			logf("%s refresh failed (keeping previous values): %v", name, err)
		}
		return true
	}
	if !refresh() {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		refresh()
	}
}
