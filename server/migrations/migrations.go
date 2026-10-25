// Package migrations holds the SQL migrations of the core and of each
// optional module that owns tables. Each directory is one module's set,
// applied by package migrate.
package migrations

import (
	"embed"

	"example.com/imkit/server/migrate"
)

//go:embed core groupjoin e2ee
var files embed.FS

// Core is the core schema: users, messages, topics, membership, push tokens,
// read cursors, group settings and the offline queues.
func Core() (migrate.Set, error) { return migrate.FromFS("core", files, "core") }

// GroupJoin is the scan-to-join module: QR tokens and join requests.
func GroupJoin() (migrate.Set, error) { return migrate.FromFS("groupjoin", files, "groupjoin") }

// E2ee is the end-to-end encryption module: key bundles and the offline
// envelope queue.
func E2ee() (migrate.Set, error) { return migrate.FromFS("e2ee", files, "e2ee") }
