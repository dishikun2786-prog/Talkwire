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

// InitTables creates the IM server tables if they don't exist
func (s *Store) InitTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS im_users (
			app_user_id VARCHAR(64) PRIMARY KEY,
			im_user_id VARCHAR(64) UNIQUE NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS im_messages (
			id BIGSERIAL PRIMARY KEY,
			topic VARCHAR(128) NOT NULL,
			seq INT NOT NULL,
			from_uid VARCHAR(64) NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			content_type VARCHAR(64) NOT NULL DEFAULT 'text',
			file_url TEXT,
			file_name VARCHAR(255),
			extra JSONB,
			duration INT DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(topic, seq)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_im_messages_topic ON im_messages(topic, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_im_messages_topic_seq ON im_messages(topic, seq DESC)`,
		`CREATE TABLE IF NOT EXISTS im_topics (
			topic VARCHAR(128) PRIMARY KEY,
			is_group BOOLEAN NOT NULL DEFAULT false,
			name VARCHAR(255) DEFAULT '',
			owner_uid VARCHAR(64) NOT NULL DEFAULT '',
			last_seq INT NOT NULL DEFAULT 0,
			clear_version INT NOT NULL DEFAULT 0,
			clear_before_seq INT NOT NULL DEFAULT 0,
			cleared_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS im_topic_members (
			topic VARCHAR(128) NOT NULL,
			uid VARCHAR(64) NOT NULL,
			role VARCHAR(20) NOT NULL DEFAULT 'member',
			joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(topic, uid)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_im_topic_members_uid ON im_topic_members(uid)`,
		// uid is intentionally NOT part of the primary key -- see the migration
		// immediately below for why a lone device token being claimable by more
		// than one uid at once was a real cross-account push-notification leak,
		// not just a theoretical one.
		`CREATE TABLE IF NOT EXISTS im_fcm_tokens (
			uid VARCHAR(64) NOT NULL,
			token TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(uid, token)
		)`,
		// Migrates the primary key from (uid, token) to (token) alone.
		//
		// A physical device only ever runs one logged-in account's push
		// notifications at a time, but the old (uid, token) key let the SAME
		// FCM token accumulate one row per account that ever logged into this
		// app on that device -- nothing ever deleted the old account's row on
		// logout (there was no unreg path at all) or on switching accounts.
		// pushOfflineRecipients selects tokens purely by uid with no idea a
		// token might currently be live under a different account, so the
		// moment account B logs in on a device account A used before, A's
		// still-offline messages start arriving in B's system notification
		// tray with A's sender name and message preview -- a real content
		// leak between two people who share (or resell) a phone.
		//
		// Making token the sole primary key turns SaveFCMToken's upsert (see
		// its doc comment) into "claiming a token retires whoever held it
		// before", which is exactly the single-current-owner invariant this
		// table needs and takes effect immediately for every already-deployed
		// client, without waiting on the companion end-of-fix that has the
		// client explicitly unreg on logout.
		//
		// Guarded so the (moderately expensive, table-locking) dedupe+rekey
		// only ever runs once: on a fresh table conkey already matches, so the
		// IF NOT EXISTS check short-circuits before either statement runs.
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conrelid = 'im_fcm_tokens'::regclass
				AND contype = 'p'
				AND conkey = ARRAY[(
					SELECT attnum FROM pg_attribute
					WHERE attrelid = 'im_fcm_tokens'::regclass AND attname = 'token'
				)]
			) THEN
				DELETE FROM im_fcm_tokens
				WHERE ctid NOT IN (
					SELECT DISTINCT ON (token) ctid FROM im_fcm_tokens
					ORDER BY token, updated_at DESC, uid DESC
				);
				ALTER TABLE im_fcm_tokens DROP CONSTRAINT IF EXISTS im_fcm_tokens_pkey;
				ALTER TABLE im_fcm_tokens ADD CONSTRAINT im_fcm_tokens_pkey PRIMARY KEY (token);
			END IF;
		END $$`,
		// im_group_settings is a 1:1 extension of im_topics (group forward/AI-QA
		// config), not a separate group table.
		//
		// ⚠️ 更正（F-21）：这里原来写着"ON DELETE CASCADE 意味着 DisbandGroup
		// 现有的成员清理就够了，不需要额外代码避免孤儿设置行"——**这句话与事实
		// 相反**。CASCADE 挂在 im_topics(topic) 上，只有删除 im_topics 那一行才
		// 会级联；而 DisbandGroup 刻意保留 im_topics 行（保住历史消息），只删
		// im_topic_members，级联因此永远不会触发。解散群后设置行原样留在库里，
		// 万一同一个 topic 字符串被复用（RandString 碰撞）或群被人工恢复，上一
		// 个群的 AI 知识库配置会直接生效。现由 DisbandGroup 显式 DELETE。
		`CREATE TABLE IF NOT EXISTS im_group_settings (
			topic VARCHAR(64) PRIMARY KEY REFERENCES im_topics(topic) ON DELETE CASCADE,
			ai_qa_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			ai_kb_ids JSONB NOT NULL DEFAULT '[]',
			ai_doc_ids JSONB NOT NULL DEFAULT '[]',
			ai_daily_quota INT NOT NULL DEFAULT 50,
			ai_cooldown_sec INT NOT NULL DEFAULT 5,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_by VARCHAR(64) NOT NULL
		)`,
		// im_pending_clears is the per-member offline-delivery queue for
		// topic_cleared: broadcastTopicCleared writes one row here for each
		// member who was offline at the moment a conversation was cleared,
		// instead of silently dropping the event. UNIQUE(topic, uid) collapses
		// repeated clears of the same topic while a member stays offline down
		// to a single row holding only the latest clear_version -- the same
		// "latest-wins, not a log" shape as im_topics.clear_version itself.
		`CREATE TABLE IF NOT EXISTS im_pending_clears (
			id BIGSERIAL PRIMARY KEY,
			topic VARCHAR(128) NOT NULL,
			uid VARCHAR(64) NOT NULL,
			wire_topic VARCHAR(128) NOT NULL,
			clear_version INT NOT NULL,
			clear_before_seq INT NOT NULL,
			from_uid VARCHAR(64) NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(topic, uid)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_im_pending_clears_uid ON im_pending_clears(uid)`,
		// im_pending_group_events is the same offline-delivery queue shape as
		// im_pending_clears, one level up: grp_sync ("你的群成员/角色变了，去刷新")
		// 和 grp_disband ("这个群没了") 此前只对当时在线的成员广播，离线成员
		// 什么都收不到，只能等自己下次主动 getGroupTopics() 才自愈——被拉进群
		// 的人在重启 App 之前根本看不到这个群。
		//
		// UNIQUE(topic, uid, kind) 让"同一个成员离线期间被反复改角色"收敛成一
		// 行：这些事件本身只是"去刷新"的提示，不携带增量，重复投递没有意义。
		// kind 参与唯一键是因为 grp_sync 与 grp_disband 语义互斥但都可能排队，
		// 不能互相覆盖（先改角色后解散，两条都得投递，且顺序由 id 保证）。
		`CREATE TABLE IF NOT EXISTS im_pending_group_events (
			id BIGSERIAL PRIMARY KEY,
			topic VARCHAR(128) NOT NULL,
			uid VARCHAR(64) NOT NULL,
			kind VARCHAR(24) NOT NULL,
			group_name VARCHAR(255) NOT NULL DEFAULT '',
			from_uid VARCHAR(64) NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(topic, uid, kind)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_im_pending_group_events_uid ON im_pending_group_events(uid)`,
		// im_read_cursors is a per-(topic,uid) read cursor, not a per-message
		// reader row: a group can have up to 500 members and a history page up
		// to 200 messages, and "who has read message X" needs to stay
		// O(members) regardless of how many messages are on screen. A message
		// with seq S counts as read by uid U iff cursor(topic,U).last_read_seq
		// >= S -- reader sets are computed at read time from this table, never
		// stored per message, which is also why a recalled message needs no
		// cleanup here: its seq just stops being queried once the message row
		// is gone.
		`CREATE TABLE IF NOT EXISTS im_read_cursors (
			topic          VARCHAR(128) NOT NULL,
			uid            VARCHAR(64) NOT NULL,
			last_read_seq  INT NOT NULL DEFAULT 0,
			updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (topic, uid)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_im_read_cursors_topic ON im_read_cursors(topic)`,
		// im_group_invite_tokens holds at most one active scan-to-join QR token
		// per group (PK = topic, upsert-on-mint == "refresh"). Unlike
		// im_group_settings this is not a config row a group may go without --
		// GetActiveGroupQrToken/MintGroupQrToken create one on first request.
		// Same F-21 caveat as im_group_settings applies: the CASCADE only fires
		// if the im_topics row itself is deleted, which DisbandGroup
		// deliberately never does -- DisbandGroup below has an explicit DELETE
		// for this table so a disbanded group's token can't outlive it.
		`CREATE TABLE IF NOT EXISTS im_group_invite_tokens (
			topic      VARCHAR(64)  PRIMARY KEY REFERENCES im_topics(topic) ON DELETE CASCADE,
			token      VARCHAR(64)  NOT NULL,
			created_by VARCHAR(64)  NOT NULL,
			created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ  NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_im_group_invite_tokens_token ON im_group_invite_tokens(token)`,
		// im_group_join_requests records a scan-to-join request awaiting
		// owner/admin review when im_group_settings.require_join_approval is
		// on. The partial unique index blocks a second concurrent "pending"
		// row for the same (topic, requester) -- a doubled tap or retry
		// collapses onto the request already awaiting review instead of
		// spamming the queue; a fresh request after a prior one was
		// approved/rejected is allowed (this index only sees status='pending'
		// rows).
		`CREATE TABLE IF NOT EXISTS im_group_join_requests (
			id            BIGSERIAL    PRIMARY KEY,
			topic         VARCHAR(64)  NOT NULL,
			requester_uid VARCHAR(64)  NOT NULL,
			message       VARCHAR(120) NOT NULL DEFAULT '',
			status        VARCHAR(16)  NOT NULL DEFAULT 'pending',
			reviewed_by   VARCHAR(64)  NOT NULL DEFAULT '',
			reviewed_at   TIMESTAMPTZ,
			created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_im_group_join_requests_topic ON im_group_join_requests(topic, status)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_im_group_join_requests_pending ON im_group_join_requests(topic, requester_uid) WHERE status = 'pending'`,

		// -- E2EE --
		//
		// im_e2e_identities holds each account's currently-published X3DH+PQXDH
		// prekey bundle. Keyed by uid (im-go's internal identifier), matching
		// every other table in this file (im_topic_members.uid,
		// im_messages.from_uid, ...) rather than the main server's app_user_id
		// UUID — resolving one from the other is an extra DB round trip these
		// handlers have no other reason to make, since client.uid is already
		// on hand for every authenticated action.
		//
		// PK on uid, not (uid, device_id): this first implementation
		// deliberately supports exactly one "current" encryption-capable
		// device per account rather than fanning a session out to every
		// device on the account the way Signal's own multi-device linking
		// does — publishing again from a second device silently supersedes
		// the first. That's a real, documented scope reduction, not an
		// oversight: encrypted threads are explicitly device-pinned/
		// non-synced already (see the design doc), so a user who switches
		// devices restarts encrypted conversations rather than migrating them.
		// All key-material columns are TEXT holding base64 — every one of them
		// is public key data or a signature over public key data, never a
		// secret, which is what makes it safe for this table to be ordinarily
		// readable server-side at all.
		`CREATE TABLE IF NOT EXISTS im_e2e_identities (
			uid                        VARCHAR(64) PRIMARY KEY,
			device_id                  INT NOT NULL DEFAULT 1,
			registration_id            INT NOT NULL,
			identity_key               TEXT NOT NULL,
			signed_pre_key_id          INT NOT NULL,
			signed_pre_key             TEXT NOT NULL,
			signed_pre_key_signature   TEXT NOT NULL,
			kyber_pre_key_id           INT NOT NULL,
			kyber_pre_key              TEXT NOT NULL,
			kyber_pre_key_signature    TEXT NOT NULL,
			updated_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// im_e2e_one_time_pre_keys is a per-uid pool of single-use Curve25519
		// prekeys, consumed exactly once by ConsumeOneE2eOneTimePreKey's
		// FOR UPDATE SKIP LOCKED pop so two people fetching this account's
		// bundle concurrently can never be handed the same one-time key.
		`CREATE TABLE IF NOT EXISTS im_e2e_one_time_pre_keys (
			id           BIGSERIAL PRIMARY KEY,
			uid          VARCHAR(64) NOT NULL,
			key_id       INT NOT NULL,
			pre_key      TEXT NOT NULL,
			consumed     BOOLEAN NOT NULL DEFAULT false,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(uid, key_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_im_e2e_otpk_available ON im_e2e_one_time_pre_keys(uid, id) WHERE consumed = false`,
		// im_e2e_envelopes is a bounded-TTL relay queue for encrypted content
		// aimed at a currently-offline recipient — see ActionE2eSend's doc
		// comment in protocol.go for why this, and not im_messages, is where
		// it lives. Rows are deleted the moment they're delivered
		// (PopE2eEnvelopesForUID) and reaped unconditionally past expires_at
		// regardless of delivery (StartE2eEnvelopeReaper) — an encrypted
		// message that nobody was ever online to receive expires the same way
		// an unanswered push notification would, rather than accumulating
		// forever as a permanent server-side copy of content the product
		// promises never becomes one.
		`CREATE TABLE IF NOT EXISTS im_e2e_envelopes (
			id          BIGSERIAL PRIMARY KEY,
			topic       VARCHAR(128) NOT NULL,
			to_uid      VARCHAR(64) NOT NULL,
			from_uid    VARCHAR(64) NOT NULL,
			kind        VARCHAR(24) NOT NULL,
			payload     TEXT NOT NULL DEFAULT '',
			msg_type    INT NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at  TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_im_e2e_envelopes_to ON im_e2e_envelopes(to_uid, id)`,
		// StartE2eEnvelopeReaper's hourly DELETE filters on expires_at alone —
		// without this it degrades to a full-table scan every hour as the
		// queue grows under sustained offline-recipient traffic (TTL is up to
		// e2ee.envelope_ttl_hours, default 14 days), unlike its sibling
		// PopE2eEnvelopesForUID which is already bounded by idx_im_e2e_envelopes_to.
		`CREATE INDEX IF NOT EXISTS idx_im_e2e_envelopes_expires ON im_e2e_envelopes(expires_at)`,
	}
	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("failed to execute: %s: %w", q[:80], err)
		}
	}
	// Existing installations created im_topics/im_messages before this
	// migration existed. Keep it idempotent because IM tables are initialized
	// by this standalone process instead of Prisma.
	for _, q := range []string{
		`ALTER TABLE im_topics ADD COLUMN IF NOT EXISTS clear_version INT NOT NULL DEFAULT 0`,
		`ALTER TABLE im_topics ADD COLUMN IF NOT EXISTS clear_before_seq INT NOT NULL DEFAULT 0`,
		`ALTER TABLE im_topics ADD COLUMN IF NOT EXISTS cleared_at TIMESTAMPTZ`,
		// encrypted is group-only in practice (see ActionE2eSetGroupEncrypted):
		// a 1:1 encrypted thread never shares a topic id with its plain
		// counterpart to begin with (it lives under a separate e2e_ topic
		// namespace that never appears in im_topics at all), so there is
		// nothing for a boolean flag to disambiguate there. Defaults false —
		// existing groups are unaffected until their owner opts in.
		`ALTER TABLE im_topics ADD COLUMN IF NOT EXISTS encrypted BOOLEAN NOT NULL DEFAULT FALSE`,
		// Presence itself stays in memory — after a restart every socket really
		// is dead, so "everyone offline" is the correct answer, not state to
		// recover. last_seen_at exists only so the UI can say "最近在线 X 分钟前"
		// instead of asserting a hard offline bit, which is far more tolerant of
		// the transient churn cross-border clients live with.
		`ALTER TABLE im_users ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ`,
		// require_join_approval gates whether group_join (scan-to-join) admits
		// immediately or files a im_group_join_requests row instead. Defaults
		// FALSE so shipping this column changes nothing for any existing group
		// until its owner explicitly opts in (hardcode-to-config-default rule).
		`ALTER TABLE im_group_settings ADD COLUMN IF NOT EXISTS require_join_approval BOOLEAN NOT NULL DEFAULT FALSE`,
		// Forward provenance + AI-virtual-sender tag. forward_from_msg_id
		// intentionally has no FK: it points at a (topic, seq) pair in a
		// *different* topic than the row it's attached to, and im_messages'
		// only unique key is the composite (topic, seq) — not something a
		// single-column FK can reference.
		`ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS forward_from_msg_id BIGINT DEFAULT NULL`,
		`ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS forward_from_topic VARCHAR(64) DEFAULT NULL`,
		`ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS forward_chain_depth SMALLINT NOT NULL DEFAULT 0`,
		`ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS sender_kind VARCHAR(16) NOT NULL DEFAULT 'user'`,
		// client_msg_id backs SaveMessageFullIdempotent's dedup -- see its doc
		// comment. Nullable and only unique when present: forwards, AI
		// replies, and any client that hasn't started sending one yet must
		// keep working unconstrained. CREATE UNIQUE INDEX IF NOT EXISTS is
		// naturally idempotent (unlike ADD CONSTRAINT, no guard needed).
		`ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS client_msg_id VARCHAR(64) DEFAULT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_im_messages_topic_client_msg_id ON im_messages(topic, client_msg_id) WHERE client_msg_id IS NOT NULL`,
		// recalled_at backs SoftDeleteMessage. A hard DELETE frees the (topic,
		// seq) pair, and GetNextSeq only ever hands out last_seq+1 -- so the
		// hole a recall leaves behind is permanent and indistinguishable, to
		// a client doing gap detection over its local seq series, from a
		// message it simply never received. It then asks for that range
		// forever and never gets an answer. Keeping the row as a tombstone
		// preserves the seq series; GetMessages/GetMessagesAfter filter it
		// out so the content really is gone for every reader.
		`ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS recalled_at TIMESTAMPTZ DEFAULT NULL`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("failed to migrate IM tables (im_topics/im_messages/im_users): %w", err)
		}
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
