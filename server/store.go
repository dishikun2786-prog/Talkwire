package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	_ "github.com/lib/pq"
	"log"
	"strings"
	"time"

	"github.com/dishikun2786-prog/Talkwire/server/migrate"
	"github.com/dishikun2786-prog/Talkwire/server/migrations"
)

// Store handles all database operations for the IM server
type Store struct {
	db *sql.DB
}

func NewStore(dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	db.SetMaxOpenConns(50)
	// 空闲连接数从 10 提到 20：本服务的负载形态是"一条群消息瞬间打散成
	// N 个成员的并发查询"（成员名册、离线 token、profile），突发并发远高于
	// 稳态。空闲池只有 10 时，每次突发里超出的那部分都要现建 TCP+TLS+认证
	// 连接，建完几毫秒后又被关掉，下一条群消息重来一遍。20 让常见规模的
	// 群消息扇出整个落在空闲池里。
	db.SetMaxIdleConns(20)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	log.Println("[Store] Connected to PostgreSQL")
	return &Store{db: db}, nil
}

// InitTables brings the schema up to date by applying the versioned migrations
// of the core and then of the installed modules (packages migrations and
// migrate). The first migration of each set is the reference server's schema
// and is idempotent, so a database that server created is adopted as it is.
func (s *Store) InitTables(moduleSets ...migrate.Set) error {
	core, err := migrations.Core()
	if err != nil {
		return err
	}
	applied, err := migrate.Apply(context.Background(), s.db, append([]migrate.Set{core}, moduleSets...)...)
	if err != nil {
		return err
	}
	for _, id := range applied {
		log.Printf("[Store] applied migration %s", id)
	}
	log.Println("[Store] Tables initialized")
	return nil
}

// BeginTx starts a raw DB transaction for callers (handleForwardBundle) that
// need several inserts to commit or roll back together. Kept as a thin
// wrapper rather than exposing s.db directly, matching how the rest of Store
// keeps sql.DB private to this file.
func (s *Store) BeginTx() (*sql.Tx, error) {
	return s.db.Begin()
}

// inPlaceholders builds "$1,$2,..." plus the matching args slice for an IN
// clause, starting at $1. Same approach GetLastSeenBatch documents: lib/pq is
// imported for its driver side effect only, so pq.Array is not in scope, and
// values stay bound rather than interpolated.
func inPlaceholders(values []string) (string, []interface{}) {
	placeholders := make([]string, len(values))
	args := make([]interface{}, len(values))
	for i, v := range values {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = v
	}
	return strings.Join(placeholders, ","), args
}

// Helper: return nil for empty string (for SQL NULL)
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Helper for creating base64url from bytes
func base64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// Ping checks database connectivity, used by the HTTP health check so
// external monitoring (and WinSW's restart-on-failure) can detect a dead DB
// connection instead of the endpoint unconditionally reporting "ok".
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

func init() {
	// Ensure sha256 is imported
	var _ = sha256.Sum256
}
