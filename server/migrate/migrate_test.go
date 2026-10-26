package migrate

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	_ "github.com/lib/pq"
)

func TestFromFS(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0002_second.sql": {Data: []byte("SELECT 2")},
		"m/0001_first.sql":  {Data: []byte("SELECT 1")},
	}
	set, err := FromFS("m", fsys, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Migrations) != 2 || set.Migrations[0].Name != "first" || set.Migrations[1].Version != 2 {
		t.Fatalf("unexpected set: %+v", set)
	}
}

func TestFromFSRejects(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"gap":       {"m/0001_a.sql": {}, "m/0003_c.sql": {}},
		"bad name":  {"m/1_a.sql": {}},
		"no start":  {"m/0002_b.sql": {}},
		"stray dir": {"m/0001_a.sql": {}, "m/sub/x.sql": {}},
	} {
		if _, err := FromFS("m", fsys, "m"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// testDB opens a fresh schema in the database named by TALKWIRE_TEST_DSN.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TALKWIRE_TEST_DSN")
	if dsn == "" {
		t.Skip("set TALKWIRE_TEST_DSN to a throwaway PostgreSQL database to run the database tests")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	if _, err := admin.Exec(`DROP SCHEMA IF EXISTS talkwire_migrate_test CASCADE; CREATE SCHEMA talkwire_migrate_test`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(`DROP SCHEMA IF EXISTS talkwire_migrate_test CASCADE`) })
	db, err := sql.Open("postgres", dsn+" search_path=talkwire_migrate_test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func set(sqls ...string) Set {
	s := Set{Module: "t"}
	for i, q := range sqls {
		s.Migrations = append(s.Migrations, Migration{Version: i + 1, Name: "m", SQL: q})
	}
	return s
}

func TestApplyOnceAndInOrder(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := set(`CREATE TABLE a (x INT)`, `ALTER TABLE a ADD COLUMN y INT`)
	applied, err := Apply(ctx, db, s)
	if err != nil || len(applied) != 2 {
		t.Fatalf("first apply: %v %v", applied, err)
	}
	applied, err = Apply(ctx, db, s)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second apply should do nothing: %v %v", applied, err)
	}
	// A later release adds a migration: only that one runs.
	s = set(`CREATE TABLE a (x INT)`, `ALTER TABLE a ADD COLUMN y INT`, `ALTER TABLE a ADD COLUMN z INT`)
	applied, err = Apply(ctx, db, s)
	if err != nil || len(applied) != 1 || applied[0] != "t/0003_m" {
		t.Fatalf("upgrade: %v %v", applied, err)
	}
}

// A database migrated before the project was renamed from ImKit keeps its
// record of applied migrations: nothing runs twice.
func TestApplyTakesOverBookkeepingFromBeforeRename(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := set(`CREATE TABLE a (x INT)`, `ALTER TABLE a ADD COLUMN y INT`)
	if _, err := db.Exec(`CREATE TABLE a (x INT);
		CREATE TABLE imkit_schema_migrations (
			module VARCHAR(64) NOT NULL, version INT NOT NULL, name VARCHAR(255) NOT NULL,
			checksum CHAR(64) NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (module, version))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO imkit_schema_migrations (module, version, name, checksum) VALUES ('t', 1, 'm', $1)`, checksum(s.Migrations[0].SQL)); err != nil {
		t.Fatal(err)
	}
	applied, err := Apply(ctx, db, s)
	if err != nil || len(applied) != 1 || applied[0] != "t/0002_m" {
		t.Fatalf("only the migration the old table lacks should run: %v %v", applied, err)
	}
	var old, renamed, pkey sql.NullString
	if err := db.QueryRow(`SELECT to_regclass('imkit_schema_migrations')::text, to_regclass('talkwire_schema_migrations')::text, to_regclass('talkwire_schema_migrations_pkey')::text`).Scan(&old, &renamed, &pkey); err != nil {
		t.Fatal(err)
	}
	if old.Valid || !renamed.Valid || !pkey.Valid {
		t.Fatalf("old table %v, new table %v, new primary key %v", old, renamed, pkey)
	}
}

func TestApplyRefusesChangedMigration(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := Apply(ctx, db, set(`CREATE TABLE a (x INT)`)); err != nil {
		t.Fatal(err)
	}
	_, err := Apply(ctx, db, set(`CREATE TABLE a (x BIGINT)`))
	if err == nil || !strings.Contains(err.Error(), "changed after it was applied") {
		t.Fatalf("changed migration accepted: %v", err)
	}
}

func TestApplyRefusesNewerDatabase(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := Apply(ctx, db, set(`CREATE TABLE a (x INT)`, `CREATE TABLE b (x INT)`)); err != nil {
		t.Fatal(err)
	}
	_, err := Apply(ctx, db, set(`CREATE TABLE a (x INT)`))
	if err == nil || !strings.Contains(err.Error(), "newer schema") {
		t.Fatalf("older binary ran against a newer schema: %v", err)
	}
}

func TestFailedMigrationLeavesNoTrace(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_, err := Apply(ctx, db, set(`CREATE TABLE a (x INT); SELECT no_such_function()`))
	if err == nil {
		t.Fatal("failing migration reported success")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM talkwire_schema_migrations`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("recorded a failed migration: n=%d err=%v", n, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM a`).Scan(&n); err == nil {
		t.Fatal("half of a failed migration was committed")
	}
}

func TestConcurrentApplyRunsEachMigrationOnce(t *testing.T) {
	db := testDB(t)
	// A migration that cannot run twice: two servers starting at once must
	// not both execute it.
	s := set(`CREATE TABLE a (x INT)`)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	total := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			applied, err := Apply(context.Background(), db, s)
			errs <- err
			total <- len(applied)
		}()
	}
	wg.Wait()
	close(errs)
	close(total)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	sum := 0
	for n := range total {
		sum += n
	}
	if sum != 1 {
		t.Fatalf("migration applied %d times", sum)
	}
}
