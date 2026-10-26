// Package migrate applies versioned schema migrations exactly once, per
// module. The core and every optional module own a set; a module that is not
// installed contributes no set, so its tables are never created.
//
// Applied migrations are recorded in talkwire_schema_migrations with a checksum
// of their SQL. Startup fails if an applied migration's SQL has changed, or if
// the database records a version this binary does not know (it was migrated
// by a newer release).
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
)

// Migration is one versioned step. Its SQL may hold several statements and
// runs in a single transaction.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Set is one module's migrations, versioned 1, 2, 3, ... without gaps.
type Set struct {
	Module     string
	Migrations []Migration
}

var fileName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// FromFS loads the files NNNN_name.sql in dir as a module's set.
func FromFS(module string, fsys fs.FS, dir string) (Set, error) {
	set := Set{Module: module}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return set, err
	}
	for _, e := range entries {
		m := fileName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			return set, fmt.Errorf("migrate: %s/%s: not a migration file name (NNNN_name.sql)", dir, e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		data, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return set, err
		}
		set.Migrations = append(set.Migrations, Migration{Version: v, Name: m[2], SQL: string(data)})
	}
	return set, set.validate()
}

func (s Set) validate() error {
	if s.Module == "" {
		return fmt.Errorf("migrate: set without a module name")
	}
	sort.Slice(s.Migrations, func(i, j int) bool { return s.Migrations[i].Version < s.Migrations[j].Version })
	for i, m := range s.Migrations {
		if m.Version != i+1 {
			return fmt.Errorf("migrate: %s: versions must run 1, 2, 3, ... without gaps; found %04d at position %d", s.Module, m.Version, i+1)
		}
	}
	return nil
}

func checksum(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

// lockKey is the advisory lock that serialises migration across processes
// sharing one database.
// The value is "imkit" in ASCII, the project's name when the key was
// chosen. It stays, so a binary from before the rename and one after it
// still exclude each other while both run against one database.
const lockKey = 0x696d6b6974

// takeOverBookkeeping renames the bookkeeping table of a database migrated
// before the project was renamed from ImKit, so its migrations are not run a
// second time.
const takeOverBookkeeping = `DO $$
BEGIN
	IF to_regclass('talkwire_schema_migrations') IS NULL AND to_regclass('imkit_schema_migrations') IS NOT NULL THEN
		ALTER TABLE imkit_schema_migrations RENAME TO talkwire_schema_migrations;
		ALTER INDEX IF EXISTS imkit_schema_migrations_pkey RENAME TO talkwire_schema_migrations_pkey;
	END IF;
END $$`

const bookkeeping = `CREATE TABLE IF NOT EXISTS talkwire_schema_migrations (
	module     VARCHAR(64)  NOT NULL,
	version    INT          NOT NULL,
	name       VARCHAR(255) NOT NULL,
	checksum   CHAR(64)     NOT NULL,
	applied_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
	PRIMARY KEY (module, version)
)`

// Apply brings the database up to date with the sets, in the order given:
// core first, then modules whose migrations depend on core tables. It returns
// the migrations it applied, as "module/NNNN_name".
func Apply(ctx context.Context, db *sql.DB, sets ...Set) ([]string, error) {
	for _, s := range sets {
		if err := s.validate(); err != nil {
			return nil, err
		}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return nil, fmt.Errorf("migrate: lock: %w", err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey) //nolint:errcheck
	if _, err := conn.ExecContext(ctx, takeOverBookkeeping); err != nil {
		return nil, fmt.Errorf("migrate: bookkeeping table from before the rename: %w", err)
	}
	if _, err := conn.ExecContext(ctx, bookkeeping); err != nil {
		return nil, fmt.Errorf("migrate: bookkeeping table: %w", err)
	}

	var applied []string
	for _, s := range sets {
		done, err := recorded(ctx, conn, s.Module)
		if err != nil {
			return applied, err
		}
		for v := range done {
			if v > len(s.Migrations) {
				return applied, fmt.Errorf("migrate: %s: database is at version %04d, this binary knows %04d; refusing to run against a newer schema", s.Module, v, len(s.Migrations))
			}
		}
		for _, m := range s.Migrations {
			id := fmt.Sprintf("%s/%04d_%s", s.Module, m.Version, m.Name)
			if sum, ok := done[m.Version]; ok {
				if sum != checksum(m.SQL) {
					return applied, fmt.Errorf("migrate: %s was changed after it was applied", id)
				}
				continue
			}
			if err := run(ctx, conn, s.Module, m); err != nil {
				return applied, fmt.Errorf("migrate: %s: %w", id, err)
			}
			applied = append(applied, id)
		}
	}
	return applied, nil
}

func recorded(ctx context.Context, conn *sql.Conn, module string) (map[int]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version, checksum FROM talkwire_schema_migrations WHERE module = $1`, module)
	if err != nil {
		return nil, fmt.Errorf("migrate: read %s: %w", module, err)
	}
	defer rows.Close()
	done := map[int]string{}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, err
		}
		done[v] = sum
	}
	return done, rows.Err()
}

func run(ctx context.Context, conn *sql.Conn, module string, m Migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO talkwire_schema_migrations (module, version, name, checksum) VALUES ($1, $2, $3, $4)`,
		module, m.Version, m.Name, checksum(m.SQL)); err != nil {
		return err
	}
	return tx.Commit()
}
