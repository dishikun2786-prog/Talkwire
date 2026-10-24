package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	_ "github.com/lib/pq"
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

// EnsureUser creates or returns the IM user ID for a given app user
func (s *Store) EnsureUser(appUserID string) (string, error) {
	imUID := IMUserID(appUserID)
	_, err := s.db.Exec(`
		INSERT INTO im_users (app_user_id, im_user_id)
		VALUES ($1, $2)
		ON CONFLICT (app_user_id) DO UPDATE
			SET im_user_id = EXCLUDED.im_user_id
	`, appUserID, imUID)
	if err != nil {
		return "", fmt.Errorf("ensure user: %w", err)
	}
	return imUID, nil
}

// GetIMUserID returns the IM user ID for an app user
func (s *Store) GetIMUserID(appUserID string) (string, error) {
	var imUID string
	err := s.db.QueryRow(`SELECT im_user_id FROM im_users WHERE app_user_id = $1`, appUserID).Scan(&imUID)
	if err == sql.ErrNoRows {
		return s.EnsureUser(appUserID)
	}
	if err != nil {
		return "", err
	}
	return imUID, nil
}

// GetAppUserID returns the app user ID from an IM user ID
func (s *Store) GetAppUserID(imUID string) (string, error) {
	var appUserID string
	err := s.db.QueryRow(`SELECT app_user_id FROM im_users WHERE im_user_id = $1`, imUID).Scan(&appUserID)
	if err != nil {
		return "", err
	}
	return appUserID, nil
}

// GetNextSeq returns the next message sequence number for a topic
func (s *Store) GetNextSeq(topic string) (int, error) {
	var seq int
	err := s.db.QueryRow(`
		INSERT INTO im_topics (topic, is_group, last_seq)
		VALUES ($1, false, 1)
		ON CONFLICT (topic) DO UPDATE
			SET last_seq = im_topics.last_seq + 1
		RETURNING im_topics.last_seq
	`, topic).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("get next seq: %w", err)
	}
	return seq, nil
}

// SaveMessage persists a message to the database
func (s *Store) SaveMessage(topic, fromUID, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int) (int, error) {
	seq, err := s.GetNextSeq(topic)
	if err != nil {
		return 0, err
	}
	// A nil json.RawMessage (the common case: no "extra" field on plain
	// text/image/voice-without-duration messages) does not reliably convert
	// to SQL NULL through database/sql's reflection-based parameter binding
	// for named []byte types — lib/pq was sending it as an empty, non-JSON
	// string and Postgres rejected it with "invalid input syntax for type
	// json", failing every send that omitted extra. Pass an explicit
	// interface{}(nil) instead, which is unambiguous.
	var extraParam interface{}
	if len(extra) > 0 {
		extraParam = []byte(extra)
	}
	_, err = s.db.Exec(`
		INSERT INTO im_messages (topic, seq, from_uid, content, content_type, file_url, file_name, extra, duration)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, topic, seq, fromUID, content, contentType, nullIfEmpty(fileURL), nullIfEmpty(fileName), extraParam, duration)
	if err != nil {
		return 0, fmt.Errorf("save message: %w", err)
	}
	return seq, nil
}

// BeginTx starts a raw DB transaction for callers (handleForwardBundle) that
// need several inserts to commit or roll back together. Kept as a thin
// wrapper rather than exposing s.db directly, matching how the rest of Store
// keeps sql.DB private to this file.
func (s *Store) BeginTx() (*sql.Tx, error) {
	return s.db.Begin()
}

// GetNextSeqTx is the transaction-scoped twin of GetNextSeq, used by
// handleForwardBundle so every seq assignment in a bundle forward is part of
// the same all-or-nothing transaction as the inserts that consume it.
func (s *Store) GetNextSeqTx(tx *sql.Tx, topic string) (int, error) {
	var seq int
	err := tx.QueryRow(`
		INSERT INTO im_topics (topic, is_group, last_seq)
		VALUES ($1, false, 1)
		ON CONFLICT (topic) DO UPDATE
			SET last_seq = im_topics.last_seq + 1
		RETURNING im_topics.last_seq
	`, topic).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("get next seq (tx): %w", err)
	}
	return seq, nil
}

// SaveMessageFull persists a message together with its sender-kind tag and
// optional forward provenance. It supersedes SaveMessage as the primitive
// behind Hub.deliverMessage (see hub.go); SaveMessage itself is left as-is
// since it's a lower-level building block with no reason to grow five more
// parameters for a single remaining caller path.
func (s *Store) SaveMessageFull(topic, fromUID, senderKind, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int, forwardFromMsgID *int, forwardFromTopic *string, forwardChainDepth int) (int, error) {
	// M6: seq allocation and the row insert now share one transaction. They
	// used to be two independent statements (GetNextSeq against the pool,
	// then SaveMessageFullTx(nil, ...) also directly against the pool) --
	// any failure in between (a save error, a crash, a dropped DB
	// connection) advanced im_topics.last_seq without ever inserting the row
	// it was allocated for, leaving a permanent seq gap on every client that
	// syncs this topic. Wrapping both in a transaction and rolling back on
	// any error means a failed save genuinely never consumed a seq at all --
	// GetNextSeqTx's UPDATE to last_seq is undone along with everything else.
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	seq, err := s.GetNextSeqTx(tx, topic)
	if err != nil {
		return 0, err
	}
	if err := s.SaveMessageFullTx(tx, topic, seq, fromUID, senderKind, content, contentType, fileURL, fileName, extra, duration, forwardFromMsgID, forwardFromTopic, forwardChainDepth); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return seq, nil
}

// SaveMessageFullTx inserts an already-seq-assigned message row, either
// inside a caller-provided transaction (tx != nil, used by
// handleForwardBundle) or directly against the pool (tx == nil, used by
// SaveMessageFull).
func (s *Store) SaveMessageFullTx(tx *sql.Tx, topic string, seq int, fromUID, senderKind, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int, forwardFromMsgID *int, forwardFromTopic *string, forwardChainDepth int) error {
	// See SaveMessage's comment: a nil json.RawMessage must become an
	// explicit interface{}(nil), not a zero-length []byte, or lib/pq sends an
	// empty non-JSON string and Postgres rejects it.
	var extraParam interface{}
	if len(extra) > 0 {
		extraParam = []byte(extra)
	}
	const q = `
		INSERT INTO im_messages (
			topic, seq, from_uid, sender_kind, content, content_type,
			file_url, file_name, extra, duration,
			forward_from_msg_id, forward_from_topic, forward_chain_depth
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`
	args := []interface{}{
		topic, seq, fromUID, senderKind, content, contentType,
		nullIfEmpty(fileURL), nullIfEmpty(fileName), extraParam, duration,
		forwardFromMsgID, forwardFromTopic, forwardChainDepth,
	}
	var err error
	if tx != nil {
		_, err = tx.Exec(q, args...)
	} else {
		_, err = s.db.Exec(q, args...)
	}
	if err != nil {
		return fmt.Errorf("save message (full): %w", err)
	}
	return nil
}

// SaveMessageFullIdempotent is SaveMessageFull's dedup-aware sibling, used by
// deliverMessage whenever the caller supplies a non-empty clientMsgID (in
// practice: only handlePub, since that's the only write path that can be
// resent by a client that never saw its own ack -- see H3 in the IM audit).
//
// The problem this closes: a pub's ack can be lost after the server already
// persisted and broadcast the message (connection drops mid-round-trip is
// the common case -- see ImWebSocketClient.kt's PUBLISH_PENDING/onClosed
// handling). The client, having no ack, cannot tell "never arrived" apart
// from "arrived, ack lost" and must be able to safely resend either way.
// Without a stable identity for "this is logically the same send", a resend
// becomes a second row with a new seq -- a duplicate message every
// recipient (and the sender, on their next history pull) sees twice.
//
// clientMsgID is that stable identity: the client's own localId, unique per
// logical send and unchanged across retries of the SAME send (see
// ChatRepository.kt's retryLocalId). Given that, "have I already saved a
// row for (topic, clientMsgID)" is exactly the question that answers "is
// this a genuine new message or a resend of one I already have".
//
// Race safety: the fast pre-check below is not itself the enforcement --
// two goroutines racing the exact same (topic, clientMsgID) is only
// possible if a client somehow fires the identical pub twice concurrently,
// which sendJsonSync's single-in-flight-per-key design shouldn't produce,
// but the DB is the actual backstop regardless: idx_im_messages_topic_
// client_msg_id (unique, partial) means the losing INSERT ... ON CONFLICT
// DO NOTHING affects zero rows, and the code below re-selects the winner's
// row inside the same transaction rather than assuming its own seq stuck.
func (s *Store) SaveMessageFullIdempotent(topic, clientMsgID, fromUID, senderKind, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int, forwardFromMsgID *int, forwardFromTopic *string, forwardChainDepth int) (seq int, ts time.Time, duplicate bool, err error) {
	// Fast path: this exact (topic, clientMsgID) has already been saved.
	// Skips burning a seq number on the overwhelmingly common case (a
	// resend of a pub whose ack was lost, not a genuine race).
	var existingSeq int
	var existingTs time.Time
	preErr := s.db.QueryRow(
		`SELECT seq, created_at FROM im_messages WHERE topic = $1 AND client_msg_id = $2`,
		topic, clientMsgID,
	).Scan(&existingSeq, &existingTs)
	if preErr == nil {
		return existingSeq, existingTs, true, nil
	}
	if preErr != sql.ErrNoRows {
		return 0, time.Time{}, false, fmt.Errorf("check existing client_msg_id: %w", preErr)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, time.Time{}, false, err
	}
	defer tx.Rollback()

	seq, err = s.GetNextSeqTx(tx, topic)
	if err != nil {
		return 0, time.Time{}, false, err
	}

	var extraParam interface{}
	if len(extra) > 0 {
		extraParam = []byte(extra)
	}
	const insertQ = `
		INSERT INTO im_messages (
			topic, seq, from_uid, sender_kind, content, content_type,
			file_url, file_name, extra, duration,
			forward_from_msg_id, forward_from_topic, forward_chain_depth,
			client_msg_id
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (topic, client_msg_id) WHERE client_msg_id IS NOT NULL DO NOTHING
		RETURNING created_at
	`
	var insertedTs time.Time
	insErr := tx.QueryRow(insertQ,
		topic, seq, fromUID, senderKind, content, contentType,
		nullIfEmpty(fileURL), nullIfEmpty(fileName), extraParam, duration,
		forwardFromMsgID, forwardFromTopic, forwardChainDepth, clientMsgID,
	).Scan(&insertedTs)

	if insErr == sql.ErrNoRows {
		// Lost the race: another connection's insert for this exact
		// clientMsgID committed between our pre-check and here. This seq
		// number is now orphaned (im_topics.last_seq was already advanced by
		// GetNextSeqTx and the transaction hasn't committed it to a row) --
		// an accepted, narrow-window cost of keeping the fast path cheap,
		// consistent with M6's documented seq-gap tolerance elsewhere in
		// this file. Re-select the winner's row so the caller still gets a
		// correct, existing seq/ts to hand back to its client rather than
		// erroring out a pub that in fact already succeeded once.
		if selErr := tx.QueryRow(
			`SELECT seq, created_at FROM im_messages WHERE topic = $1 AND client_msg_id = $2`,
			topic, clientMsgID,
		).Scan(&existingSeq, &existingTs); selErr != nil {
			return 0, time.Time{}, false, fmt.Errorf("re-select after conflict: %w", selErr)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return 0, time.Time{}, false, commitErr
		}
		return existingSeq, existingTs, true, nil
	}
	if insErr != nil {
		return 0, time.Time{}, false, fmt.Errorf("save message (idempotent): %w", insErr)
	}
	if err := tx.Commit(); err != nil {
		return 0, time.Time{}, false, err
	}
	return seq, insertedTs, false, nil
}

// ForwardableMessage carries the fields needed to duplicate a message into
// another topic as a forward, plus its own forward_chain_depth so the caller
// can enforce the depth limit before re-forwarding it further.
type ForwardableMessage struct {
	Seq               int
	FromUID           string
	SenderKind        string
	Content           string
	ContentType       string
	FileURL           string
	FileName          string
	Extra             json.RawMessage
	Duration          int
	ForwardChainDepth int
}

// GetMessageForForward reads a single message's forward-relevant fields by
// (topic, seq) — the unit both forward_bundle (sourceMsgIds) and
// forward_broadcast (sourceMsgId) address messages by, since that's the only
// identifier the client ever sees for a message. Returns sql.ErrNoRows
// (wrapped) when not found; callers translate that into "source_not_found".
//
// `recalled_at IS NULL` for the same reason the read paths carry it, but
// with a sharper consequence: a recalled row is stripped to an empty
// content and a NULL file_url, so without this predicate forwarding a
// message the sender just recalled would silently succeed and insert a
// blank message into the target topic instead of reporting
// "source_not_found".
func (s *Store) GetMessageForForward(topic string, seq int) (*ForwardableMessage, error) {
	var m ForwardableMessage
	var extra sql.NullString
	err := s.db.QueryRow(`
		SELECT seq, from_uid, sender_kind, content, content_type,
		       COALESCE(file_url, ''), COALESCE(file_name, ''),
		       extra, duration, forward_chain_depth
		FROM im_messages
		WHERE topic = $1 AND seq = $2 AND recalled_at IS NULL
	`, topic, seq).Scan(&m.Seq, &m.FromUID, &m.SenderKind, &m.Content, &m.ContentType,
		&m.FileURL, &m.FileName, &extra, &m.Duration, &m.ForwardChainDepth)
	if err != nil {
		return nil, err
	}
	if extra.Valid && extra.String != "" && extra.String != "null" {
		m.Extra = json.RawMessage(extra.String)
	}
	return &m, nil
}

// GroupAiSettings is the per-group configuration for the "@小桃子" group QA
// feature (see im_group_settings). AiDailyQuota/AiCooldownSec are ops-level
// knobs with column defaults; the update_group_ai_settings WS action only
// lets the group owner edit AiQaEnabled/AiKbIds/AiDocIds (see
// UpsertGroupAiSettings).
type GroupAiSettings struct {
	Topic         string
	AiQaEnabled   bool
	AiKbIds       []string
	AiDocIds      []string
	AiDailyQuota  int
	AiCooldownSec int
}

// defaultGroupAiSettings mirrors im_group_settings' column defaults, used for
// the (overwhelmingly common) case of a group that has never had its AI QA
// settings touched and therefore has no row yet.
func defaultGroupAiSettings(topic string) *GroupAiSettings {
	return &GroupAiSettings{
		Topic: topic, AiQaEnabled: false,
		AiKbIds: []string{}, AiDocIds: []string{},
		AiDailyQuota: 50, AiCooldownSec: 5,
	}
}

// GetGroupAiSettings reads a group's AI QA config, or the documented defaults
// if the group has never configured it. Callers needing repeated reads
// should go through Hub.GetGroupAiSettingsCached instead of hitting this
// directly on every message.
func (s *Store) GetGroupAiSettings(topic string) (*GroupAiSettings, error) {
	var g GroupAiSettings
	var kbIds, docIds []byte
	err := s.db.QueryRow(`
		SELECT topic, ai_qa_enabled, ai_kb_ids, ai_doc_ids, ai_daily_quota, ai_cooldown_sec
		FROM im_group_settings WHERE topic = $1
	`, topic).Scan(&g.Topic, &g.AiQaEnabled, &kbIds, &docIds, &g.AiDailyQuota, &g.AiCooldownSec)
	if err == sql.ErrNoRows {
		return defaultGroupAiSettings(topic), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get group ai settings: %w", err)
	}
	if err := json.Unmarshal(kbIds, &g.AiKbIds); err != nil || g.AiKbIds == nil {
		g.AiKbIds = []string{}
	}
	if err := json.Unmarshal(docIds, &g.AiDocIds); err != nil || g.AiDocIds == nil {
		g.AiDocIds = []string{}
	}
	return &g, nil
}

// UpsertGroupAiSettings creates or updates a group's AI QA configuration.
// updatedBy is the caller's uid; the WS handler (handleUpdateGroupAiSettings)
// must already have verified that uid is the group owner before calling
// this — this method itself does not re-check ownership.
func (s *Store) UpsertGroupAiSettings(topic string, enabled bool, kbIds, docIds []string, updatedBy string) error {
	if kbIds == nil {
		kbIds = []string{}
	}
	if docIds == nil {
		docIds = []string{}
	}
	kbJSON, err := json.Marshal(kbIds)
	if err != nil {
		return fmt.Errorf("marshal ai_kb_ids: %w", err)
	}
	docJSON, err := json.Marshal(docIds)
	if err != nil {
		return fmt.Errorf("marshal ai_doc_ids: %w", err)
	}
	// ai_daily_quota/ai_cooldown_sec are deliberately left out of the SET
	// clause: this action only lets a group owner edit enabled/kbIds/docIds,
	// not the ops-level quota/cooldown, and ON CONFLICT DO UPDATE without
	// naming a column leaves it unchanged.
	_, err = s.db.Exec(`
		INSERT INTO im_group_settings (topic, ai_qa_enabled, ai_kb_ids, ai_doc_ids, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (topic) DO UPDATE SET
			ai_qa_enabled = EXCLUDED.ai_qa_enabled,
			ai_kb_ids = EXCLUDED.ai_kb_ids,
			ai_doc_ids = EXCLUDED.ai_doc_ids,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
	`, topic, enabled, kbJSON, docJSON, updatedBy)
	if err != nil {
		return fmt.Errorf("upsert group ai settings: %w", err)
	}
	return nil
}

// GroupJoinSettings is the per-group scan-to-join approval configuration
// (see im_group_settings.require_join_approval). Same table as
// GroupAiSettings, different column -- kept as its own struct/functions
// because the two settings have different owner-only vs owner+admin
// authorization tiers at the WS handler layer.
type GroupJoinSettings struct {
	Topic           string
	RequireApproval bool
}

func defaultGroupJoinSettings(topic string) *GroupJoinSettings {
	return &GroupJoinSettings{Topic: topic, RequireApproval: false}
}

// GetGroupJoinSettings reads whether a group currently requires approval to
// join, or the documented default (false) if the group has never touched
// any im_group_settings row yet.
func (s *Store) GetGroupJoinSettings(topic string) (*GroupJoinSettings, error) {
	var g GroupJoinSettings
	err := s.db.QueryRow(`
		SELECT topic, require_join_approval FROM im_group_settings WHERE topic = $1
	`, topic).Scan(&g.Topic, &g.RequireApproval)
	if err == sql.ErrNoRows {
		return defaultGroupJoinSettings(topic), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get group join settings: %w", err)
	}
	return &g, nil
}

// UpsertGroupJoinSettings creates or updates require_join_approval only --
// ON CONFLICT DO UPDATE names just this column, so a group's AI-QA settings
// (or vice versa, UpsertGroupAiSettings touching this same row) are never
// clobbered by the other feature's write, same pattern as this file's
// ai_daily_quota/ai_cooldown_sec omission above. The WS handler
// (handleUpdateGroupJoinSettings) must already have verified the caller is
// the group owner before calling this -- this method does not re-check.
func (s *Store) UpsertGroupJoinSettings(topic string, requireApproval bool, updatedBy string) error {
	_, err := s.db.Exec(`
		INSERT INTO im_group_settings (topic, require_join_approval, updated_by, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (topic) DO UPDATE SET
			require_join_approval = EXCLUDED.require_join_approval,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
	`, topic, requireApproval, updatedBy)
	if err != nil {
		return fmt.Errorf("upsert group join settings: %w", err)
	}
	return nil
}

// GroupInviteToken is a group's currently active scan-to-join QR token.
type GroupInviteToken struct {
	Topic     string
	Token     string
	CreatedBy string
	ExpiresAt time.Time
}

// generateGroupQrToken returns a cryptographically random, URL-safe token.
// Unlike RandString (math/rand, seeded once at startup, used only for
// cosmetic grp_ topic-id suffixes -- see hub.go), a group QR token backs
// group_join when require_join_approval is off: it IS the entire access
// control for joining the group, so it must be genuinely unguessable. 24
// random bytes -> 32 base64url characters.
func generateGroupQrToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate group qr token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// GetActiveGroupQrToken returns the group's current token if one exists and
// has not expired, or (nil, nil) if the caller should mint a fresh one.
func (s *Store) GetActiveGroupQrToken(topic string) (*GroupInviteToken, error) {
	var t GroupInviteToken
	err := s.db.QueryRow(`
		SELECT topic, token, created_by, expires_at
		FROM im_group_invite_tokens WHERE topic = $1 AND expires_at > NOW()
	`, topic).Scan(&t.Topic, &t.Token, &t.CreatedBy, &t.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active group qr token: %w", err)
	}
	return &t, nil
}

// MintGroupQrToken generates a fresh token and replaces whatever the group
// had before (upsert on the topic PK -- a group has at most one live token
// at a time). "Refreshing" a group's QR code is exactly this: the old token
// row is overwritten and can never resolve again, no separate revocation
// step needed.
func (s *Store) MintGroupQrToken(topic, createdBy string, ttl time.Duration) (*GroupInviteToken, error) {
	token, err := generateGroupQrToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(ttl)
	_, err = s.db.Exec(`
		INSERT INTO im_group_invite_tokens (topic, token, created_by, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (topic) DO UPDATE SET
			token = EXCLUDED.token,
			created_by = EXCLUDED.created_by,
			created_at = NOW(),
			expires_at = EXCLUDED.expires_at
	`, topic, token, createdBy, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("mint group qr token: %w", err)
	}
	return &GroupInviteToken{Topic: topic, Token: token, CreatedBy: createdBy, ExpiresAt: expiresAt}, nil
}

// ResolveGroupQrToken maps a scanned token back to its group topic, or
// ("", false, nil) if the token does not exist or has expired -- an old,
// refreshed-away token failing to resolve is the intended revocation
// mechanism, not a bug.
func (s *Store) ResolveGroupQrToken(token string) (topic string, ok bool, err error) {
	err = s.db.QueryRow(`
		SELECT topic FROM im_group_invite_tokens WHERE token = $1 AND expires_at > NOW()
	`, token).Scan(&topic)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("resolve group qr token: %w", err)
	}
	return topic, true, nil
}

// Join-request lifecycle states. Terminal states (approved/rejected) are
// never deleted -- ReviewGroupJoinRequest's WHERE status='pending' clause
// depends on a prior review's row still being there to correctly reject a
// second concurrent review of the same request.
const (
	JoinRequestPending  = "pending"
	JoinRequestApproved = "approved"
	JoinRequestRejected = "rejected"
)

// ErrDuplicateJoinRequest is returned by CreateGroupJoinRequest when the
// requester already has a pending request on this topic -- the caller
// should treat this as "you already applied, please wait", not an error.
var ErrDuplicateJoinRequest = errors.New("a pending join request already exists")

// GroupJoinRequest is one scan-to-join request, pending or already reviewed.
type GroupJoinRequest struct {
	ID           int64
	Topic        string
	RequesterUID string
	Message      string
	Status       string
	ReviewedBy   string
	CreatedAt    time.Time
}

// CreateGroupJoinRequest files a new join request. lib/pq is imported only
// for its driver side effect in this file (see the GetLastSeenBatch comment
// above), so rather than type-asserting a *pq.Error this checks the unique
// index name lib/pq puts in the error text -- exactly the same
// duplicate-collapse contract Postgres already enforces via
// idx_im_group_join_requests_pending, just translated into a friendly
// sentinel error instead of a raw driver error string.
func (s *Store) CreateGroupJoinRequest(topic, requesterUID, message string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO im_group_join_requests (topic, requester_uid, message)
		VALUES ($1, $2, $3)
		RETURNING id
	`, topic, requesterUID, message).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "idx_im_group_join_requests_pending") {
			return 0, ErrDuplicateJoinRequest
		}
		return 0, fmt.Errorf("create group join request: %w", err)
	}
	return id, nil
}

// GetPendingGroupJoinRequests lists a group's requests awaiting review,
// oldest first so an admin clears a backlog in submission order.
func (s *Store) GetPendingGroupJoinRequests(topic string) ([]GroupJoinRequest, error) {
	rows, err := s.db.Query(`
		SELECT id, topic, requester_uid, message, status, reviewed_by, created_at
		FROM im_group_join_requests WHERE topic = $1 AND status = $2
		ORDER BY id ASC
	`, topic, JoinRequestPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupJoinRequest
	for rows.Next() {
		var r GroupJoinRequest
		if err := rows.Scan(&r.ID, &r.Topic, &r.RequesterUID, &r.Message, &r.Status, &r.ReviewedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReviewGroupJoinRequest transitions one pending request to approved or
// rejected. The WHERE status=$4 clause is the concurrency guard: if two
// admins review the same request at once, only the first UPDATE matches any
// rows -- the second gets (nil, nil) back and the caller must treat that as
// "someone already reviewed this", not silently re-apply their own verdict.
func (s *Store) ReviewGroupJoinRequest(id int64, approve bool, reviewedBy string) (*GroupJoinRequest, error) {
	status := JoinRequestRejected
	if approve {
		status = JoinRequestApproved
	}
	var r GroupJoinRequest
	err := s.db.QueryRow(`
		UPDATE im_group_join_requests
		SET status = $2, reviewed_by = $3, reviewed_at = NOW()
		WHERE id = $1 AND status = $4
		RETURNING id, topic, requester_uid, message, status, reviewed_by, created_at
	`, id, status, reviewedBy, JoinRequestPending).Scan(&r.ID, &r.Topic, &r.RequesterUID, &r.Message, &r.Status, &r.ReviewedBy, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("review group join request: %w", err)
	}
	return &r, nil
}

// GetMessages retrieves messages for a topic with pagination.
//
// `recalled_at IS NULL` is what makes SoftDeleteMessage's tombstone rows
// invisible to every reader: the row survives only to keep the (topic, seq)
// series gap-free, its content must never come back out of here. Any new
// message-reading query added to this file needs the same predicate — a
// missing one is not a compile error, it silently un-recalls messages.
func (s *Store) GetMessages(topic string, beforeSeq, limit int) ([]StoredMessage, error) {
	// 归一化只有一处实现（config.go 的 normalizeHistoryLimit），hub.handleGet
	// 也调同一个。此前这里是独立硬编码的 `>100 就压成 50`，跑在 hub 的判据之
	// 后，于是 101..200 被 hub 放行、到这里静默变成 50，且把 group_chat
	// .history_page_size 调过 100 完全不生效。这里保留一次调用是防御性的——
	// 本方法也可能被 hub 以外的调用方用到——但不要在这里另写上限。
	limit = normalizeHistoryLimit(limit)
	var rows *sql.Rows
	var err error
	if beforeSeq > 0 {
		rows, err = s.db.Query(`
			SELECT seq, from_uid, content, content_type,
			       COALESCE(file_url, ''), COALESCE(file_name, ''),
			       (EXTRACT(EPOCH FROM created_at) * 1000)::bigint,
			       extra, duration, sender_kind, forward_from_msg_id, forward_from_topic
			FROM im_messages
			WHERE topic = $1 AND seq < $2 AND recalled_at IS NULL
			ORDER BY seq DESC LIMIT $3
		`, topic, beforeSeq, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT seq, from_uid, content, content_type,
			       COALESCE(file_url, ''), COALESCE(file_name, ''),
			       (EXTRACT(EPOCH FROM created_at) * 1000)::bigint,
			       extra, duration, sender_kind, forward_from_msg_id, forward_from_topic
			FROM im_messages
			WHERE topic = $1 AND recalled_at IS NULL
			ORDER BY seq DESC LIMIT $2
		`, topic, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var msgs []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var extra sql.NullString
		var forwardFromMsgID sql.NullInt64
		var forwardFromTopic sql.NullString
		err := rows.Scan(&m.Seq, &m.From, &m.Content, &m.ContentType,
			&m.FileURL, &m.FileName, &m.Timestamp, &extra, &m.Duration,
			&m.SenderKind, &forwardFromMsgID, &forwardFromTopic)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.Topic = topic
		if extra.Valid && extra.String != "" && extra.String != "null" {
			m.Extra = json.RawMessage(extra.String)
		}
		if forwardFromMsgID.Valid {
			m.ForwardFromMsgID = int(forwardFromMsgID.Int64)
		}
		if forwardFromTopic.Valid {
			m.ForwardFromTopic = forwardFromTopic.String
		}
		msgs = append(msgs, m)
	}
	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// GetMessagesAfter retrieves messages newer than afterSeq in server sequence
// order. This is the reconnect/incremental-sync counterpart to GetMessages;
// using beforeSeq with (latest local seq + 1) only returned already cached rows
// and made offline messages silently disappear.
//
// Carries the same `recalled_at IS NULL` predicate as GetMessages — see there.
// A client that was offline when a message was both sent and recalled must
// come back to find nothing, not to find the recalled message.
func (s *Store) GetMessagesAfter(topic string, afterSeq, limit int) ([]StoredMessage, error) {
	// 同 GetMessages：唯一的归一化实现在 config.go 的 normalizeHistoryLimit。
	limit = normalizeHistoryLimit(limit)
	rows, err := s.db.Query(`
		SELECT seq, from_uid, content, content_type,
		       COALESCE(file_url, ''), COALESCE(file_name, ''),
		       (EXTRACT(EPOCH FROM created_at) * 1000)::bigint,
		       extra, duration, sender_kind, forward_from_msg_id, forward_from_topic
		FROM im_messages
		WHERE topic = $1 AND seq > $2 AND recalled_at IS NULL
		ORDER BY seq ASC LIMIT $3
	`, topic, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("get messages after: %w", err)
	}
	defer rows.Close()

	var msgs []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var extra sql.NullString
		var forwardFromMsgID sql.NullInt64
		var forwardFromTopic sql.NullString
		if err := rows.Scan(&m.Seq, &m.From, &m.Content, &m.ContentType,
			&m.FileURL, &m.FileName, &m.Timestamp, &extra, &m.Duration,
			&m.SenderKind, &forwardFromMsgID, &forwardFromTopic); err != nil {
			return nil, fmt.Errorf("scan message after: %w", err)
		}
		m.Topic = topic
		if extra.Valid && extra.String != "" && extra.String != "null" {
			m.Extra = json.RawMessage(extra.String)
		}
		if forwardFromMsgID.Valid {
			m.ForwardFromMsgID = int(forwardFromMsgID.Int64)
		}
		if forwardFromTopic.Valid {
			m.ForwardFromTopic = forwardFromTopic.String
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages after: %w", err)
	}
	return msgs, nil
}

// ClearTopicMessages hard-deletes every current message in a topic while
// recording a durable clear version/cutoff for offline-client reconciliation.
// It also returns distinct media URLs so the caller can garbage-collect blobs
// once no remaining message references them.
func (s *Store) ClearTopicMessages(topic string) (ClearTopicResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return ClearTopicResult{}, err
	}
	defer tx.Rollback()

	// Lock the topic before reading last_seq. SaveMessage increments last_seq in
	// the same row, so a concurrent publication will happen wholly before or
	// after the clear and never be accidentally deleted midway through it.
	var lastSeq, oldClearVersion int
	err = tx.QueryRow(`
		SELECT last_seq, clear_version FROM im_topics WHERE topic = $1 FOR UPDATE
	`, topic).Scan(&lastSeq, &oldClearVersion)
	if err == sql.ErrNoRows {
		return ClearTopicResult{}, fmt.Errorf("topic not found")
	}
	if err != nil {
		return ClearTopicResult{}, err
	}

	// H10: UNION (not UNION ALL) both dedupes within each SELECT and across
	// the two — a video's own file_url and its thumbUrl end up in one flat,
	// deduplicated list. $1 is bound once; Postgres lets a numbered
	// placeholder repeat within a query without a second positional arg.
	rows, err := tx.Query(`
		SELECT DISTINCT file_url FROM im_messages
		WHERE topic = $1 AND file_url IS NOT NULL AND file_url <> ''
		UNION
		SELECT DISTINCT extra->>'thumbUrl' FROM im_messages
		WHERE topic = $1 AND extra->>'thumbUrl' IS NOT NULL AND extra->>'thumbUrl' <> ''
	`, topic)
	if err != nil {
		return ClearTopicResult{}, err
	}
	var urls []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return ClearTopicResult{}, err
		}
		urls = append(urls, u)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ClearTopicResult{}, err
	}
	if err := rows.Close(); err != nil {
		return ClearTopicResult{}, err
	}

	var deleted int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM im_messages WHERE topic = $1`, topic).Scan(&deleted); err != nil {
		return ClearTopicResult{}, err
	}
	if _, err := tx.Exec(`DELETE FROM im_messages WHERE topic = $1`, topic); err != nil {
		return ClearTopicResult{}, err
	}

	clearVersion := oldClearVersion + 1
	if _, err := tx.Exec(`
		UPDATE im_topics
		SET clear_version = $2, clear_before_seq = $3, cleared_at = NOW(), updated_at = NOW()
		WHERE topic = $1
	`, topic, clearVersion, lastSeq); err != nil {
		return ClearTopicResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ClearTopicResult{}, err
	}
	return ClearTopicResult{
		DeletedMessages: deleted,
		DeletedMedia:    len(urls),
		ClearVersion:    clearVersion,
		ClearBeforeSeq:  lastSeq,
		FileURLs:        urls,
	}, nil
}

// CountFileRefs reports how many messages still reference a media blob URL, so
// a recall/clear only asks the main server to delete the underlying file once
// nothing points at it anymore (media can be shared via forwarding).
//
// H10: also matches extra->>'thumbUrl' — gcMediaBlob now gets called with
// thumbnail URLs too (see ClearTopicMessages), and forwarding a
// video reuses the same uploaded thumbnail blob across messages exactly like
// it reuses file_url. Checking file_url alone would undercount a thumbnail
// that's still alive only because ANOTHER message references it as its
// thumbUrl, and delete it out from under that still-live message.
func (s *Store) CountFileRefs(fileURL string) (int, error) {
	var n int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM im_messages WHERE file_url = $1 OR extra->>'thumbUrl' = $1
	`, fileURL).Scan(&n)
	return n, err
}

// GetUserTopics returns all topics a user is subscribed to
// TouchLastSeen records when a uid's last connection went away. Called from
// the presence worker, never from the hub loop.
func (s *Store) TouchLastSeen(uid string) error {
	_, err := s.db.Exec(`UPDATE im_users SET last_seen_at = NOW() WHERE im_user_id = $1`, uid)
	return err
}

// GetLastSeenBatch returns uid -> Unix epoch MILLISECONDS for the uids it can
// find. Epoch millis rather than a formatted timestamp on purpose: there is
// then no date format to agree on between Postgres, Go and Android, and no
// timezone to get wrong. Missing/never-seen uids are simply absent from the map.
func (s *Store) GetLastSeenBatch(uids []string) (map[string]int64, error) {
	out := make(map[string]int64, len(uids))
	if len(uids) == 0 {
		return out, nil
	}
	// Built with explicit placeholders rather than ANY($1): lib/pq is imported
	// for its driver side effect only (`_ "github.com/lib/pq"`), so pq.Array is
	// not in scope and pulling it in would change this file's dependency shape
	// for one query. Values are still bound, never interpolated.
	placeholders := make([]string, len(uids))
	args := make([]interface{}, len(uids))
	for i, u := range uids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = u
	}
	rows, err := s.db.Query(`
		SELECT im_user_id, COALESCE(EXTRACT(EPOCH FROM last_seen_at) * 1000, 0)::BIGINT
		FROM im_users
		WHERE last_seen_at IS NOT NULL AND im_user_id IN (`+strings.Join(placeholders, ",")+`)
	`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid string
		var ms int64
		if err := rows.Scan(&uid, &ms); err != nil {
			return out, err
		}
		out[uid] = ms
	}
	return out, rows.Err()
}

// GetPeerUIDs returns every uid that shares at least one topic with uid —
// covering both 1:1 peers and groupmates. This is what backs a `pres` query
// for topic:"*", so a client can reconcile its whole presence picture in one
// round trip after a reconnect instead of relying on having caught every push.
func (s *Store) GetPeerUIDs(uid string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT m2.uid
		FROM im_topic_members m1
		JOIN im_topic_members m2 ON m2.topic = m1.topic AND m2.uid <> m1.uid
		WHERE m1.uid = $1
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var peers []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	return peers, rows.Err()
}

func (s *Store) GetUserTopics(uid string) ([]TopicInfo, error) {
	rows, err := s.db.Query(`
		SELECT t.topic, t.last_seq, t.is_group, t.clear_version, t.clear_before_seq
		FROM im_topics t
		JOIN im_topic_members m ON t.topic = m.topic
		WHERE m.uid = $1
		ORDER BY t.updated_at DESC
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []TopicInfo
	for rows.Next() {
		var t TopicInfo
		if err := rows.Scan(&t.Topic, &t.LastSeq, &t.IsGroup, &t.ClearVersion, &t.ClearBeforeSeq); err != nil {
			return nil, err
		}
		topics = append(topics, t)
	}
	return topics, nil
}

// GetAdminTopics returns compact summaries for conversations that contain uid.
// It is called only via the private main-API bridge after an administrator has
// selected an app user; no end-user endpoint reaches this method.
//
// Both LATERAL subqueries carry `recalled_at IS NULL` -- see GetMessages for
// why every message read needs it. They were missing it: the preview column
// could sit on a message the sender had already recalled (showing its content
// to an administrator after it had been withdrawn from every participant), and
// the message count included tombstone rows, so a conversation whose entire
// visible history was recalled still reported a non-zero total. Being an
// admin-only path does not change the answer -- recall means the content is
// gone, not merely hidden from peers.
func (s *Store) GetAdminTopics(uid string, limit int) ([]AdminTopicInfo, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT t.topic, t.is_group, COALESCE(t.name, ''), t.last_seq,
		       t.clear_version, t.clear_before_seq,
		       COALESCE(last_msg.content, ''), COALESCE(last_msg.content_type, ''),
		       COALESCE(EXTRACT(EPOCH FROM last_msg.created_at)::bigint * 1000, 0),
		       COALESCE(msg_count.count, 0)
		FROM im_topics t
		JOIN im_topic_members own ON own.topic = t.topic AND own.uid = $1
		LEFT JOIN LATERAL (
			SELECT content, content_type, created_at
			FROM im_messages WHERE topic = t.topic AND recalled_at IS NULL
			ORDER BY seq DESC LIMIT 1
		) last_msg ON TRUE
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS count FROM im_messages
			WHERE topic = t.topic AND recalled_at IS NULL
		) msg_count ON TRUE
		ORDER BY COALESCE(last_msg.created_at, t.updated_at) DESC
		LIMIT $2
	`, uid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []AdminTopicInfo
	for rows.Next() {
		var t AdminTopicInfo
		if err := rows.Scan(&t.Topic, &t.IsGroup, &t.Name, &t.LastSeq,
			&t.ClearVersion, &t.ClearBeforeSeq, &t.LastContent, &t.LastContentType,
			&t.LastMessageAt, &t.MessageCount); err != nil {
			return nil, err
		}
		members, err := s.GetTopicMemberUIDs(t.Topic)
		if err != nil {
			return nil, err
		}
		t.Members = members
		topics = append(topics, t)
	}
	return topics, rows.Err()
}

// GetAdminTopic reads membership and clear state for authorization checks in
// the main API before it renders or clears a conversation.
func (s *Store) GetAdminTopic(topic string) (*AdminTopicInfo, error) {
	var t AdminTopicInfo
	err := s.db.QueryRow(`
		SELECT topic, is_group, COALESCE(name, ''), last_seq, clear_version, clear_before_seq
		FROM im_topics WHERE topic = $1
	`, topic).Scan(&t.Topic, &t.IsGroup, &t.Name, &t.LastSeq, &t.ClearVersion, &t.ClearBeforeSeq)
	if err != nil {
		return nil, err
	}
	members, err := s.GetTopicMemberUIDs(topic)
	if err != nil {
		return nil, err
	}
	t.Members = members
	return &t, nil
}

// GetAdminMessages pages history newest-first under optional server-side
// filters. Returning one extra row makes hasMore accurate without an expensive
// COUNT(*) over every message in a large conversation.
func (s *Store) GetAdminMessages(topic string, beforeSeq, limit int, kind, keyword string, fromMs, toMs int64) ([]StoredMessage, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	// recalled_at IS NULL seeds the filter list rather than being appended
	// later, so every optional filter below narrows an already-recall-free set.
	// It was absent entirely, which put recalled message bodies back on the
	// admin IM-records page -- including via the keyword filter, which would
	// happily ILIKE against content the sender had withdrawn.
	where := []string{"topic = $1", "recalled_at IS NULL"}
	args := []interface{}{topic}
	next := 2
	add := func(condition string, value interface{}) {
		where = append(where, fmt.Sprintf(condition, next))
		args = append(args, value)
		next++
	}
	if beforeSeq > 0 {
		add("seq < $%d", beforeSeq)
	}
	switch kind {
	case "text":
		where = append(where, "content_type = 'text'")
	case "image", "video", "audio":
		where = append(where, "content_type LIKE '"+kind+"/%'")
	case "file":
		where = append(where, "content_type NOT IN ('text', 'system') AND content_type NOT LIKE 'image/%' AND content_type NOT LIKE 'video/%' AND content_type NOT LIKE 'audio/%'")
	}
	if keyword != "" {
		add("content ILIKE '%%' || $%d || '%%'", keyword)
	}
	if fromMs > 0 {
		add("created_at >= to_timestamp($%d / 1000.0)", fromMs)
	}
	if toMs > 0 {
		add("created_at <= to_timestamp($%d / 1000.0)", toMs)
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(`
		SELECT seq, from_uid, content, content_type,
		       COALESCE(file_url, ''), COALESCE(file_name, ''),
		       (EXTRACT(EPOCH FROM created_at) * 1000)::bigint,
		       extra, duration, sender_kind, forward_from_msg_id, forward_from_topic
		FROM im_messages
		WHERE %s
		ORDER BY seq DESC LIMIT $%d
	`, strings.Join(where, " AND "), next)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var messages []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var extra sql.NullString
		var forwardFromMsgID sql.NullInt64
		var forwardFromTopic sql.NullString
		if err := rows.Scan(&m.Seq, &m.From, &m.Content, &m.ContentType,
			&m.FileURL, &m.FileName, &m.Timestamp, &extra, &m.Duration,
			&m.SenderKind, &forwardFromMsgID, &forwardFromTopic); err != nil {
			return nil, false, err
		}
		m.Topic = topic
		if extra.Valid && extra.String != "" && extra.String != "null" {
			m.Extra = json.RawMessage(extra.String)
		}
		if forwardFromMsgID.Valid {
			m.ForwardFromMsgID = int(forwardFromMsgID.Int64)
		}
		if forwardFromTopic.Valid {
			m.ForwardFromTopic = forwardFromTopic.String
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	// The timeline is rendered chronologically even though this query pages
	// backwards from the most recent sequence.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, hasMore, nil
}

// CreateGroup creates a new group topic
func (s *Store) CreateGroup(ownerUID, name string, members []string) (string, error) {
	topic := "grp_" + RandString(12)
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// Create topic
	_, err = tx.Exec(`
		INSERT INTO im_topics (topic, is_group, name, owner_uid, last_seq)
		VALUES ($1, true, $2, $3, 0)
	`, topic, name, ownerUID)
	if err != nil {
		return "", fmt.Errorf("create group topic: %w", err)
	}

	// Add the creator as the group's owner row. Role passed as a bind
	// parameter rather than inlined so RoleOwner stays the single source of
	// truth for the literal (see its doc comment in protocol.go).
	_, err = tx.Exec(`
		INSERT INTO im_topic_members (topic, uid, role)
		VALUES ($1, $2, $3)
	`, topic, ownerUID, RoleOwner)
	if err != nil {
		return "", fmt.Errorf("add owner to group: %w", err)
	}

	// Add other members
	for _, m := range members {
		if m == ownerUID {
			continue
		}
		_, err = tx.Exec(`
			INSERT INTO im_topic_members (topic, uid, role)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING
		`, topic, m, RoleMember)
		if err != nil {
			return "", fmt.Errorf("add member %s: %w", m, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return topic, nil
}

// GetGroupInfo returns group details
func (s *Store) GetGroupInfo(topic string) (*GroupInfo, error) {
	var info GroupInfo
	err := s.db.QueryRow(`
		SELECT topic, name, owner_uid FROM im_topics WHERE topic = $1 AND is_group = true
	`, topic).Scan(&info.Topic, &info.Name, &info.Owner)
	if err != nil {
		return nil, err
	}

	// H8: this used to be treated as "non-fatal" and return &info with
	// Members left at its zero value (0). handleLeaveGrp's owner-leaving
	// branch reads exactly that field to decide "is this genuinely the last
	// member, safe to auto-disband" vs "must transfer ownership first" — a
	// transient COUNT failure was therefore indistinguishable from a real
	// empty group, and silently disbanded groups that still had members.
	// Every call site here already branches on err != nil and degrades
	// gracefully (skip using the info / fall back), so propagating the error
	// is strictly safer than fabricating a misleading Members=0.
	err = s.db.QueryRow(`SELECT COUNT(*) FROM im_topic_members WHERE topic = $1`, topic).Scan(&info.Members)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// GetGroupMembers returns all members of a group
func (s *Store) GetGroupMembers(topic string) ([]GroupMemberInfo, error) {
	rows, err := s.db.Query(`
		SELECT uid, role, EXTRACT(EPOCH FROM joined_at)::bigint * 1000
		FROM im_topic_members WHERE topic = $1 ORDER BY joined_at ASC
	`, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []GroupMemberInfo
	for rows.Next() {
		var m GroupMemberInfo
		if err := rows.Scan(&m.UID, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

// AddGroupMember adds a user to a group as a plain member, and is a no-op
// for anyone already in it.
//
// F-22: the ON CONFLICT clause used to be `DO UPDATE SET role = 'member'`,
// which silently demoted whoever it touched. Every "put this uid in this
// group" path runs through here -- invite, and any future ensure-member
// helper -- so re-inviting someone who was already a member (a completely
// ordinary double-tap or stale-roster action) stripped the owner of
// ownership, or an admin of their admin rights, with no error and no trace.
// Worse, im_topics.owner_uid was left untouched, so the group ended up with
// an owner_uid pointing at a row whose role said 'member': CanManageGroup's
// role check and IsGroupOwner's owner_uid check then disagreed about who
// runs the group.
//
// DO NOTHING is the correct semantic here -- "add if absent". Promotion and
// demotion have their own explicit, owner-gated entry point
// (SetGroupMemberRole) and must never be a side effect of an add.
func (s *Store) AddGroupMember(topic, uid string) error {
	_, err := s.db.Exec(`
		INSERT INTO im_topic_members (topic, uid, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (topic, uid) DO NOTHING
	`, topic, uid, RoleMember)
	return err
}

// RemoveGroupMember removes a user from a group, along with any pending
// per-member queue rows for them on that topic -- once they're no longer a
// member, GetTopicMemberUIDs will never enumerate them again, so leftover
// rows would just sit there forever.
//
// The im_pending_group_events cleanup matters for more than tidiness: a
// queued grp_sync for a topic this uid has just been kicked out of would,
// on their next login, tell their client to go refresh a group it is no
// longer allowed to read — a guaranteed-to-fail fetch presented to the user
// as a phantom group reappearing in their list.
func (s *Store) RemoveGroupMember(topic, uid string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM im_topic_members WHERE topic = $1 AND uid = $2`, topic, uid); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM im_pending_clears WHERE topic = $1 AND uid = $2`, topic, uid); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM im_pending_group_events WHERE topic = $1 AND uid = $2 AND kind = $3`, topic, uid, PendingGroupEventSync); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveP2PMembers revokes both sides' membership on a P2P topic without
// touching im_topics or im_messages -- unfriending must not delete history
// (see InternalContactSync's doc comment), but it must revoke the
// authorization gate that im_topic_members actually is. resolveP2PTopic
// treats "the caller currently has a membership row" as proof of an
// already-verified friendship and skips the AreFriends round trip on that
// basis; leaving these rows in place after an unfriend/reject/block event
// would make that skip permanent, letting a removed contact keep messaging,
// reading full history, and even hard-deleting the shared conversation via
// del_topic. Idempotent: safe to call even if the pair never had a topic.
func (s *Store) RemoveP2PMembers(topic, uidA, uidB string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, uid := range []string{uidA, uidB} {
		if _, err := tx.Exec(`DELETE FROM im_topic_members WHERE topic = $1 AND uid = $2`, topic, uid); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM im_pending_clears WHERE topic = $1 AND uid = $2`, topic, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IsGroupMember checks if a user is a member of a group
func (s *Store) IsGroupMember(topic, uid string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM im_topic_members WHERE topic = $1 AND uid = $2`, topic, uid).Scan(&count)
	return count > 0, err
}

// IsGroupOwner checks if a user is the owner of a group
func (s *Store) IsGroupOwner(topic, uid string) (bool, error) {
	var ownerUID string
	err := s.db.QueryRow(`SELECT owner_uid FROM im_topics WHERE topic = $1`, topic).Scan(&ownerUID)
	if err != nil {
		return false, err
	}
	return ownerUID == uid, nil
}

// GetMemberRole returns this uid's role in the topic, or ("", nil) when they
// are not a member at all. The no-row case is deliberately NOT an error:
// "not a member" is a perfectly ordinary answer to this question and every
// caller has to handle it anyway, so making it look like a database failure
// only tempts callers into treating a genuine DB outage as "just not a
// member" (fail-open on an authorization check).
func (s *Store) GetMemberRole(topic, uid string) (string, error) {
	var role string
	err := s.db.QueryRow(
		`SELECT role FROM im_topic_members WHERE topic = $1 AND uid = $2`,
		topic, uid,
	).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

// CanManageGroup answers the one question every group-management entry point
// (kick, update name, clear conversation, ...) needs: may this uid act on
// this group? True for the owner and for admins.
//
// The owner arm checks im_topics.owner_uid rather than the membership row's
// role, and does so as a plain OR alongside the role check, on purpose:
// those two sources of truth can disagree (that is precisely what F-22's
// silent demotion produced), and a management check should recognize the
// owner if EITHER says so. IsGroupOwner keeps using owner_uid alone as the
// single strict definition of ownership for the operations only an owner may
// perform.
//
// Errors are propagated, never swallowed into false: a caller that cannot
// tell "not allowed" from "database unreachable" will eventually be written
// to fail one of them the wrong way.
func (s *Store) CanManageGroup(topic, uid string) (bool, error) {
	var can bool
	err := s.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM im_topics WHERE topic = $1 AND owner_uid = $2 AND owner_uid <> ''
		) OR EXISTS(
			SELECT 1 FROM im_topic_members WHERE topic = $1 AND uid = $2 AND role = $3
		)
	`, topic, uid, RoleAdmin).Scan(&can)
	if err != nil {
		return false, err
	}
	return can, nil
}

// SetGroupMemberRole promotes a member to admin or demotes an admin back to
// member. It refuses to write anything other than those two roles, and never
// touches the owner's row.
//
// Two guards, both load-bearing:
//
//   - The Go-side whitelist. role reaches here from a client-supplied
//     `memberRole` field; without it a client could write role = 'owner' (or
//     any junk string) into a membership row, producing a second owner row
//     that disagrees with im_topics.owner_uid. Ownership moves only through
//     TransferGroupOwnership, which updates both places in one transaction.
//   - `AND role <> RoleOwner` in the SQL. Even a correct caller can race:
//     ownership can transfer between the caller's permission check and this
//     UPDATE. Without this predicate that race demotes the sitting owner to
//     member while im_topics.owner_uid still names them -- a group whose
//     owner is not an owner.
//
// A zero-row result is an error rather than a silent success: it means the
// target is not a member, or is the owner, or the group is gone. Reporting
// that as success would show the caller's UI a promotion that never happened.
func (s *Store) SetGroupMemberRole(topic, uid, role string) error {
	if role != RoleAdmin && role != RoleMember {
		return fmt.Errorf("set member role: refusing to assign role %q", role)
	}
	res, err := s.db.Exec(`
		UPDATE im_topic_members SET role = $3
		WHERE topic = $1 AND uid = $2 AND role <> $4
	`, topic, uid, role, RoleOwner)
	if err != nil {
		return fmt.Errorf("set member role: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set member role: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("set member role: %s is not an assignable member of %s", uid, topic)
	}
	return nil
}

// CountGroupAdmins returns how many admin rows a group currently has, so the
// promote path can enforce a ceiling. The owner is not counted -- they are
// not an admin row and cannot be demoted, so including them would make the
// configured limit mean one fewer actual admin than it says.
func (s *Store) CountGroupAdmins(topic string) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM im_topic_members WHERE topic = $1 AND role = $2`,
		topic, RoleAdmin,
	).Scan(&n)
	return n, err
}

// IsGroupTopic reports whether a topic exists and is a group.
//
// A missing topic answers (false, nil) -- same reasoning as GetMemberRole's
// no-row case. Callers use this to reject group-only actions on P2P topics;
// the `grp_` prefix is a naming convention, this is the stored fact, and
// authorization decisions should rest on the latter.
func (s *Store) IsGroupTopic(topic string) (bool, error) {
	var isGroup bool
	err := s.db.QueryRow(`SELECT is_group FROM im_topics WHERE topic = $1`, topic).Scan(&isGroup)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return isGroup, nil
}

// SoftDeleteMessage is the recall primitive. It replaced a hard-DELETE
// predecessor (DeleteMessage), which has since been removed outright rather
// than left sitting here unused: a dead function whose whole job is to punch a
// permanent hole in a topic's seq series is one grep-by-name away from being
// picked up again, and a deprecation comment does not stop that.
//
// It keeps the row -- and therefore the (topic, seq) slot --
// while wiping everything the row carried, and returns the media URL the
// message referenced so the caller can garbage-collect the blob.
//
// Why a tombstone instead of a DELETE. Seq numbers come from
// im_topics.last_seq and are never reissued, so a hard delete punches a
// permanent hole in a topic's seq series. A client doing gap detection over
// its local messages cannot tell that hole apart from a message it never
// received: it asks for the missing range on every reconnect, forever, and
// is answered with nothing forever. Keeping the row makes the series
// contiguous again; GetMessages/GetMessagesAfter filter `recalled_at IS
// NULL` so no reader ever sees it.
//
// The row is stripped (content, file_url, file_name, extra) rather than just
// flagged, so "recall leaves nothing on the server" still holds literally,
// and so CountFileRefs stops counting this row as a reference to the blob --
// otherwise the tombstone would pin every recalled message's media alive.
//
// Old values are read through a CTE because a plain UPDATE ... RETURNING
// hands back the NEW (already-cleared) values. Data-modifying CTEs always
// run to completion regardless of whether the outer query reads their
// output, so the update is not conditional on the SELECT.
//
// ⚠️ 已知缺口（此前那个硬删版本的 H10 修复覆盖了这一点，本函数没有）：
// 视频消息的首帧缩略图是
// 另一个独立上传的 blob，只存在于 extra->>'thumbUrl' 里。本函数按跨端契约只
// 返回一个 URL，缩略图 blob 因此不会被调用方回收，会继续留在 MinIO/本地盘上
// 且可通过未鉴权的 /images/:filename 下载。要补齐需要把返回值改成 []string，
// 属于跨端契约变更，不在本轮范围内。
func (s *Store) SoftDeleteMessage(topic string, seq int, fromUID string) (string, bool, error) {
	var fileURL string
	err := s.db.QueryRow(`
		WITH victim AS (
			SELECT topic, seq, COALESCE(file_url, '') AS old_file_url
			FROM im_messages
			WHERE topic = $1 AND seq = $2 AND from_uid = $3 AND recalled_at IS NULL
		), wiped AS (
			UPDATE im_messages m
			SET recalled_at = NOW(),
			    content = '',
			    file_url = NULL,
			    file_name = NULL,
			    extra = NULL
			FROM victim v
			WHERE m.topic = v.topic AND m.seq = v.seq
			RETURNING 1
		)
		SELECT old_file_url FROM victim
	`, topic, seq, fromUID).Scan(&fileURL)
	if err == sql.ErrNoRows {
		// Not found, not this caller's message, or already recalled. All
		// three are "nothing to do", not failures -- a duplicate recall from
		// a retrying client must stay idempotent.
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return fileURL, true, nil
}

// UpdateGroupName updates the group display name
func (s *Store) UpdateGroupName(topic, name string) error {
	_, err := s.db.Exec(`UPDATE im_topics SET name = $1, updated_at = NOW() WHERE topic = $2`, name, topic)
	return err
}

// TransferGroupOwnership hands the owner role from fromUID to toUID. The
// WHERE owner_uid = fromUID guard makes this atomically safe even if the
// caller's earlier IsGroupOwner check has since gone stale (0 rows affected
// means fromUID is no longer owner).
func (s *Store) TransferGroupOwnership(topic, fromUID, toUID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE im_topics SET owner_uid = $1, updated_at = NOW() WHERE topic = $2 AND owner_uid = $3`, toUID, topic, fromUID)
	if err != nil {
		return fmt.Errorf("transfer ownership: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("transfer ownership: %s is not the current owner of %s", fromUID, topic)
	}

	// D3: the outgoing owner drops all the way to plain member, not to admin.
	// Handing over ownership is meant to be a real hand-off of control; if the
	// old owner kept management rights, "转让群主" would not actually give
	// anything up. The new owner can promote them back via set_member_role if
	// that is what both sides want.
	if _, err := tx.Exec(`UPDATE im_topic_members SET role = $3 WHERE topic = $1 AND uid = $2`, topic, fromUID, RoleMember); err != nil {
		return fmt.Errorf("demote old owner: %w", err)
	}
	if _, err := tx.Exec(`UPDATE im_topic_members SET role = $3 WHERE topic = $1 AND uid = $2`, topic, toUID, RoleOwner); err != nil {
		return fmt.Errorf("promote new owner: %w", err)
	}

	return tx.Commit()
}

// DisbandGroup removes every member of a group topic. im_topics/im_messages
// rows are intentionally kept (message history stays intact for anyone with
// a local cache), but with no members left, IsGroupMember/IsGroupOwner/
// GetUserGroups/GetUserTopics/handlePub's membership check all naturally
// stop recognizing the topic for everyone — no separate "disbanded" flag
// needed.
//
// F-21: two things this used to leave behind, both consequences of the
// deliberate decision to keep the im_topics row.
//
//  1. im_group_settings. Its ON DELETE CASCADE hangs off im_topics(topic),
//     which is exactly the row we do NOT delete — so the cascade never fires
//     and the group's AI-QA config (enabled flag, knowledge-base ids, quota)
//     outlived the group forever. The comment on that table's DDL asserted
//     the opposite; it has been corrected.
//  2. im_topics.owner_uid. Left pointing at the ex-owner even though their
//     membership row is gone, so IsGroupOwner("disbanded topic", oldOwner)
//     still answers true while IsGroupMember answers false — two
//     authorization helpers disagreeing about a dead group. Resetting it to
//     the column's own empty-string default makes every owner check fail
//     closed.
//
// Deliberately NOT deleting the im_topics row itself: it also carries
// last_seq, and dropping it would let GetNextSeq restart this topic's seq
// series at 1 while clients still hold cached messages at higher seqs.
func (s *Store) DisbandGroup(topic string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("disband group: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM im_topic_members WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM im_pending_clears WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM im_group_settings WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group (settings): %w", err)
	}
	// Same F-21 shape as im_group_settings above: im_group_invite_tokens' and
	// im_group_join_requests' ON DELETE CASCADE / lack thereof both hang off
	// im_topics(topic), which this function deliberately never deletes -- so
	// both must be cleaned up explicitly or a disbanded group's QR token
	// stays scannable and its stale join requests stay listable forever.
	if _, err := tx.Exec(`DELETE FROM im_group_invite_tokens WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group (invite tokens): %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM im_group_join_requests WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group (join requests): %w", err)
	}
	if _, err := tx.Exec(`UPDATE im_topics SET owner_uid = '', updated_at = NOW() WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group (clear owner): %w", err)
	}
	// Queued "go refresh this group" nudges and queued "someone applied to
	// join" notices for a group that no longer exists are both dead weight —
	// the grp_disband event the caller enqueues right after this supersedes
	// them. Scoped to these two kinds precisely so it can never eat that
	// disband row itself, whichever order the two happen in.
	if _, err := tx.Exec(
		`DELETE FROM im_pending_group_events WHERE topic = $1 AND kind IN ($2, $3)`,
		topic, PendingGroupEventSync, PendingGroupEventJoinRequest,
	); err != nil {
		return fmt.Errorf("disband group (pending events): %w", err)
	}
	return tx.Commit()
}

// GetUserGroups returns all groups a user belongs to
func (s *Store) GetUserGroups(uid string) ([]GroupInfo, error) {
	rows, err := s.db.Query(`
		SELECT t.topic, t.name, t.owner_uid, t.encrypted,
			(SELECT COUNT(*) FROM im_topic_members m WHERE m.topic = t.topic)
		FROM im_topics t
		JOIN im_topic_members m ON t.topic = m.topic
		WHERE m.uid = $1 AND t.is_group = true
		ORDER BY t.updated_at DESC
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []GroupInfo
	for rows.Next() {
		var g GroupInfo
		if err := rows.Scan(&g.Topic, &g.Name, &g.Owner, &g.Encrypted, &g.Members); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// GetP2PTopic returns a deterministic topic for two-user conversation
// Topic format: p2p_<sorted(uidA, uidB) hash>
func (s *Store) GetP2PTopic(uidA, uidB string) string {
	pair := []string{uidA, uidB}
	sort.Strings(pair)
	key := pair[0] + ":" + pair[1]
	h := sha256.Sum256([]byte(key))
	return "p2p_" + base64url(h[:8])
}

// EnsureP2PTopic creates a P2P topic if it doesn't exist and adds both users as members
func (s *Store) EnsureP2PTopic(uidA, uidB string) (string, error) {
	topic := s.GetP2PTopic(uidA, uidB)

	// Create topic if not exists
	_, err := s.db.Exec(`
		INSERT INTO im_topics (topic, is_group, name, last_seq)
		VALUES ($1, false, '', 0)
		ON CONFLICT (topic) DO NOTHING
	`, topic)
	if err != nil {
		return topic, fmt.Errorf("ensure p2p topic: %w", err)
	}

	// Add both users as members
	for _, uid := range []string{uidA, uidB} {
		_, err = s.db.Exec(`
			INSERT INTO im_topic_members (topic, uid, role)
			VALUES ($1, $2, $3)
			ON CONFLICT (topic, uid) DO NOTHING
		`, topic, uid, RoleMember)
		if err != nil {
			return topic, fmt.Errorf("add member to p2p: %w", err)
		}
	}

	return topic, nil
}

// PendingClear is one durable "you missed this clear while offline" tombstone
// row, holding everything deliverPendingClears needs to replay it exactly as
// though it had been broadcastTopicCleared to an online client.
type PendingClear struct {
	ID             int64
	WireTopic      string
	ClearVersion   int
	ClearBeforeSeq int
	FromUID        string
}

// EnqueuePendingClear upserts a per-member pending-clear tombstone for a
// member who was offline at the moment broadcastTopicCleared ran. The
// UNIQUE(topic, uid) constraint plus the WHERE clause below collapse "the
// same topic cleared more than once while this member stays offline" down to
// a single row holding only the latest clearVersion -- mirroring how
// im_topics.clear_version itself is a single latest-wins value, not a log.
func (s *Store) EnqueuePendingClear(topic, uid, wireTopic string, clearVersion, clearBeforeSeq int, fromUID string) error {
	_, err := s.db.Exec(`
		INSERT INTO im_pending_clears (topic, uid, wire_topic, clear_version, clear_before_seq, from_uid)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (topic, uid) DO UPDATE SET
			wire_topic = EXCLUDED.wire_topic,
			clear_version = EXCLUDED.clear_version,
			clear_before_seq = EXCLUDED.clear_before_seq,
			from_uid = EXCLUDED.from_uid,
			created_at = NOW()
		WHERE EXCLUDED.clear_version > im_pending_clears.clear_version
	`, topic, uid, wireTopic, clearVersion, clearBeforeSeq, fromUID)
	return err
}

// GetPendingClears returns every clear this uid still needs to be told about,
// oldest first. Capped so a pathological backlog can't stall a login.
func (s *Store) GetPendingClears(uid string) ([]PendingClear, error) {
	rows, err := s.db.Query(`
		SELECT id, wire_topic, clear_version, clear_before_seq, from_uid
		FROM im_pending_clears WHERE uid = $1 ORDER BY id ASC LIMIT 500
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingClear
	for rows.Next() {
		var p PendingClear
		if err := rows.Scan(&p.ID, &p.WireTopic, &p.ClearVersion, &p.ClearBeforeSeq, &p.FromUID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AckPendingClear deletes one delivered tombstone, scoped to uid so a client
// can never ack (and silently drop) another user's pending clear by guessing
// an id.
func (s *Store) AckPendingClear(id int64, uid string) error {
	_, err := s.db.Exec(`DELETE FROM im_pending_clears WHERE id = $1 AND uid = $2`, id, uid)
	return err
}

// ReapExpiredPendingClears drops tombstones nobody has ack'd within maxAge --
// the backstop for clients too old to understand ack_topic_cleared. Safe to
// drop unconditionally: any client that eventually reconnects and re-fetches
// that topic still gets the authoritative clear_version/clear_before_seq from
// im_topics via handleGet, independent of this queue.
func (s *Store) ReapExpiredPendingClears(maxAge time.Duration) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM im_pending_clears WHERE created_at < NOW() - ($1 * INTERVAL '1 second')`,
		int(maxAge.Seconds()),
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// StartPendingClearsReaper runs ReapExpiredPendingClears on an hourly tick.
// Launch with `go` once from main.go, mirroring hub.gcForwardReqCache's
// launch pattern.
func (s *Store) StartPendingClearsReaper() {
	const maxAge = 30 * 24 * time.Hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		n, err := s.ReapExpiredPendingClears(maxAge)
		if err != nil {
			logf("reapPendingClears: %v", err)
			continue
		}
		if n > 0 {
			logf("reapPendingClears: dropped %d expired pending clear(s)", n)
		}
	}
}

// ReadCursor is one member's read position in a group topic: everything up
// to and including Seq is considered read by UID.
type ReadCursor struct {
	UID string
	Seq int
}

// UpsertReadCursor advances uid's read cursor in topic to seq, monotonically.
// The WHERE clause makes this safe against out-of-order delivery: a "note
// read" that arrives late for an already-superseded (lower) seq is a no-op,
// never a regression. Scoped to the caller (group_chat.read_receipt_enabled
// gate lives in the handler, not here) so this function itself has no
// opinion on whether the feature is on.
func (s *Store) UpsertReadCursor(topic, uid string, seq int) error {
	_, err := s.db.Exec(`
		INSERT INTO im_read_cursors (topic, uid, last_read_seq, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (topic, uid) DO UPDATE SET
			last_read_seq = GREATEST(im_read_cursors.last_read_seq, EXCLUDED.last_read_seq),
			updated_at = NOW()
		WHERE EXCLUDED.last_read_seq > im_read_cursors.last_read_seq
	`, topic, uid, seq)
	return err
}

// GetReadCursors returns every member's read cursor for topic, for a client
// that just opened the conversation (or reconnected) to build a baseline
// without waiting for live "note read" broadcasts it may have missed while
// offline. Bounded by group_chat.max_members (500 today), so this is always
// a small, PK-indexed scan.
func (s *Store) GetReadCursors(topic string) ([]ReadCursor, error) {
	rows, err := s.db.Query(`
		SELECT uid, last_read_seq FROM im_read_cursors WHERE topic = $1
	`, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadCursor
	for rows.Next() {
		var c ReadCursor
		if err := rows.Scan(&c.UID, &c.Seq); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PendingGroupEvent is one queued "your group membership changed while you
// were offline" notice, holding everything deliverPendingGroupEvents needs
// to replay the push exactly as an online member would have received it.
type PendingGroupEvent struct {
	ID        int64
	Topic     string
	UID       string
	Kind      string
	GroupName string
	FromUID   string
}

// EnqueuePendingGroupEvent records a grp_sync / grp_disband notice for a
// member who was offline when it fired.
//
// These pushes used to be online-only: a member who was added to a group,
// had their role changed, or whose group was disbanded while disconnected
// received nothing at all, and only found out on their next unrelated full
// refresh -- in practice, after killing and reopening the app.
//
// ON CONFLICT collapses repeats rather than queueing a second row: the
// payload is a "go refresh" nudge with no delta in it, so N identical
// notices and one notice are worth exactly the same to the client. The
// refreshed created_at keeps the reaper from expiring a notice that is
// still being re-raised.
func (s *Store) EnqueuePendingGroupEvent(topic, uid, kind, groupName, fromUID string) error {
	if kind != PendingGroupEventSync && kind != PendingGroupEventDisband {
		return fmt.Errorf("enqueue pending group event: unknown kind %q", kind)
	}
	_, err := s.db.Exec(`
		INSERT INTO im_pending_group_events (topic, uid, kind, group_name, from_uid)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (topic, uid, kind) DO UPDATE SET
			group_name = EXCLUDED.group_name,
			from_uid = EXCLUDED.from_uid,
			created_at = NOW()
	`, topic, uid, kind, groupName, fromUID)
	return err
}

// GetPendingGroupEvents returns everything this uid still needs to be told
// about, oldest first so a grp_sync that preceded a grp_disband is replayed
// in that order. Capped so a pathological backlog cannot stall a login.
func (s *Store) GetPendingGroupEvents(uid string) ([]PendingGroupEvent, error) {
	rows, err := s.db.Query(`
		SELECT id, topic, uid, kind, group_name, from_uid
		FROM im_pending_group_events WHERE uid = $1 ORDER BY id ASC LIMIT 500
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingGroupEvent
	for rows.Next() {
		var e PendingGroupEvent
		if err := rows.Scan(&e.ID, &e.Topic, &e.UID, &e.Kind, &e.GroupName, &e.FromUID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AckPendingGroupEvent deletes one delivered notice, scoped to the uid that
// is acking it.
//
// ⚠️ 修复记录：这里原来没有 uid 参数，导致任何客户端猜到别人的行 id 就能替
// 对方 ack 掉一条通知——对方因此收不到它，要等下次 getGroupTopics() 全量
// 刷新才自愈。补法是把 uid 加进签名和 WHERE 子句（本次新增的
// grp_join_request/grp_join_reviewed 两种事件正好是新增调用点，顺手一并
// 修掉，不再制造第二个同类缺口）；hub.go 的 handleAckPendingGroupEvent 调用方
// 已同步改为传入 client 自己的 uid，不是消息体里的字段。
func (s *Store) AckPendingGroupEvent(id int64, uid string) error {
	_, err := s.db.Exec(`DELETE FROM im_pending_group_events WHERE id = $1 AND uid = $2`, id, uid)
	return err
}

// ReapExpiredPendingGroupEvents drops notices nobody acked within maxAge --
// the backstop for clients too old to understand ack_group_event. Safe to
// drop unconditionally: group membership itself lives in im_topic_members,
// and a client that reconnects and calls getGroupTopics() gets the
// authoritative picture regardless of this queue. The queue only shortens
// the window before that happens.
func (s *Store) ReapExpiredPendingGroupEvents(maxAge time.Duration) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM im_pending_group_events WHERE created_at < NOW() - ($1 * INTERVAL '1 second')`,
		int(maxAge.Seconds()),
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// StartPendingGroupEventsReaper runs ReapExpiredPendingGroupEvents hourly.
// Launch with `go` once from main.go, alongside StartPendingClearsReaper.
func (s *Store) StartPendingGroupEventsReaper() {
	const maxAge = 30 * 24 * time.Hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		n, err := s.ReapExpiredPendingGroupEvents(maxAge)
		if err != nil {
			logf("reapPendingGroupEvents: %v", err)
			continue
		}
		if n > 0 {
			logf("reapPendingGroupEvents: dropped %d expired group event(s)", n)
		}
	}
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

// GetFCMTokensBatch resolves push tokens for many uids in one round trip.
//
// The per-uid GetFCMTokens is fine for a 1:1 message, but the offline-push
// path for a group message calls it once per offline member: a 200-person
// group with most members offline turned one incoming message into ~200
// sequential queries, each taking a pooled connection, on the hot path of
// every single group send. uids with no registered device are simply absent
// from the map.
func (s *Store) GetFCMTokensBatch(uids []string) (map[string][]string, error) {
	out := make(map[string][]string, len(uids))
	if len(uids) == 0 {
		return out, nil
	}
	ph, args := inPlaceholders(uids)
	rows, err := s.db.Query(`SELECT uid, token FROM im_fcm_tokens WHERE uid IN (`+ph+`)`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid, token string
		if err := rows.Scan(&uid, &token); err != nil {
			return out, err
		}
		out[uid] = append(out[uid], token)
	}
	return out, rows.Err()
}

// GetAppUserIDBatch maps IM uids to app user UUIDs in one round trip, the
// batch twin of GetAppUserID. Same motivation as GetFCMTokensBatch: the push
// path needs an app user id per recipient to resolve display names, and was
// doing that lookup one query at a time per offline group member. uids with
// no im_users row are absent from the map rather than mapped to "".
func (s *Store) GetAppUserIDBatch(uids []string) (map[string]string, error) {
	out := make(map[string]string, len(uids))
	if len(uids) == 0 {
		return out, nil
	}
	ph, args := inPlaceholders(uids)
	rows, err := s.db.Query(`SELECT im_user_id, app_user_id FROM im_users WHERE im_user_id IN (`+ph+`)`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var imUID, appUserID string
		if err := rows.Scan(&imUID, &appUserID); err != nil {
			return out, err
		}
		out[imUID] = appUserID
	}
	return out, rows.Err()
}

// SaveFCMToken registers a device token for push notifications, reassigning
// it away from whatever uid held it before. token is the primary key (see
// the migration in InitTables), so this ON CONFLICT clause is what actually
// enforces "a device token belongs to one logged-in account at a time" --
// the previous owner's row is overwritten, not left to accumulate alongside
// this one.
func (s *Store) SaveFCMToken(uid, token string) error {
	_, err := s.db.Exec(`
		INSERT INTO im_fcm_tokens (uid, token) VALUES ($1, $2)
		ON CONFLICT (token) DO UPDATE SET uid = EXCLUDED.uid, updated_at = NOW()
	`, uid, token)
	return err
}

// DeleteFCMToken unregisters one device token for one uid, scoped to that
// uid so a stale unreg request racing a fresher SaveFCMToken for a
// different uid on the same token can't delete the wrong owner's row.
// Called on client-initiated logout (unreg_token) and is a no-op if the
// token was already reassigned or removed.
func (s *Store) DeleteFCMToken(uid, token string) error {
	_, err := s.db.Exec(`DELETE FROM im_fcm_tokens WHERE uid = $1 AND token = $2`, uid, token)
	return err
}

// DeleteFCMTokenByValue removes a device token regardless of which uid
// currently owns it. Used when FCM itself reports the token as
// unregistered (app uninstalled, token rotated) -- at that point the token
// is dead everywhere, not just for the uid we happened to be pushing to.
func (s *Store) DeleteFCMTokenByValue(token string) error {
	_, err := s.db.Exec(`DELETE FROM im_fcm_tokens WHERE token = $1`, token)
	return err
}

// TopicExists checks whether a topic (P2P or group) has already been
// created. No longer used to gate resolveP2PTopic's friendship check --
// that now checks the caller's own im_topic_members row via IsGroupMember,
// since gating on topic existence let a revoked P2P friendship's membership
// rows get silently re-inserted by the next EnsureP2PTopic call (see
// resolveP2PTopic's doc comment). Still used elsewhere for lighter existence
// checks that don't carry that authorization weight.
func (s *Store) TopicExists(topic string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM im_topics WHERE topic = $1)`, topic).Scan(&exists)
	return exists, err
}

// GetLastSeq returns the last sequence number for a topic (from cache)
func (s *Store) GetLastSeq(topic string) (int, error) {
	var seq int
	err := s.db.QueryRow(`SELECT COALESCE(last_seq, 0) FROM im_topics WHERE topic = $1`, topic).Scan(&seq)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return seq, err
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

// GetTopicMemberUIDs returns all member UIDs for a topic (P2P or group) —
// used to find offline recipients for push notifications.
func (s *Store) GetTopicMemberUIDs(topic string) ([]string, error) {
	rows, err := s.db.Query(`SELECT uid FROM im_topic_members WHERE topic = $1`, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uids []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		uids = append(uids, uid)
	}
	return uids, nil
}

// GetGroupManagerUIDs returns a group's owner and all current admins --
// exactly the set authorized by ensureGroupManager, used to fan out
// join-request review notifications to everyone who can act on one.
func (s *Store) GetGroupManagerUIDs(topic string) ([]string, error) {
	rows, err := s.db.Query(`SELECT uid FROM im_topic_members WHERE topic = $1 AND role IN ($2, $3)`, topic, RoleOwner, RoleAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uids []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		uids = append(uids, uid)
	}
	return uids, rows.Err()
}

// GetFCMTokens returns all registered device push tokens for a user.
func (s *Store) GetFCMTokens(uid string) ([]string, error) {
	rows, err := s.db.Query(`SELECT token FROM im_fcm_tokens WHERE uid = $1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, nil
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

// -- E2EE --

// E2eIdentityRow is the currently-published X3DH+PQXDH bundle for one
// account, minus the one-time prekey (that's fetched separately and
// atomically by ConsumeOneE2eOneTimePreKey — see its doc comment for why
// it can't just be a column here).
type E2eIdentityRow struct {
	DeviceID              int
	RegistrationID        int
	IdentityKey           string
	SignedPreKeyID        int
	SignedPreKey          string
	SignedPreKeySignature string
	KyberPreKeyID         int
	KyberPreKey           string
	KyberPreKeySignature  string
}

// UpsertE2eIdentity replaces uid's published bundle and tops up its one-time
// prekey pool. A device republishing supersedes whatever it (or an earlier
// device on the same account — see im_e2e_identities' doc comment)
// published before; old one-time keys are left alone rather than wiped,
// since a key already handed out via ConsumeOneE2eOneTimePreKey may still be
// mid-use by a session someone is establishing right now.
func (s *Store) UpsertE2eIdentity(uid string, deviceID int, p *E2ePublishBundlePayload) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO im_e2e_identities (
			uid, device_id, registration_id, identity_key,
			signed_pre_key_id, signed_pre_key, signed_pre_key_signature,
			kyber_pre_key_id, kyber_pre_key, kyber_pre_key_signature, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW())
		ON CONFLICT (uid) DO UPDATE SET
			device_id = EXCLUDED.device_id,
			registration_id = EXCLUDED.registration_id,
			identity_key = EXCLUDED.identity_key,
			signed_pre_key_id = EXCLUDED.signed_pre_key_id,
			signed_pre_key = EXCLUDED.signed_pre_key,
			signed_pre_key_signature = EXCLUDED.signed_pre_key_signature,
			kyber_pre_key_id = EXCLUDED.kyber_pre_key_id,
			kyber_pre_key = EXCLUDED.kyber_pre_key,
			kyber_pre_key_signature = EXCLUDED.kyber_pre_key_signature,
			updated_at = NOW()
	`, uid, deviceID, p.RegistrationId, p.IdentityKey,
		p.SignedPreKeyId, p.SignedPreKey, p.SignedPreKeySignature,
		p.KyberPreKeyId, p.KyberPreKey, p.KyberPreKeySignature); err != nil {
		return fmt.Errorf("upsert identity: %w", err)
	}

	for _, otpk := range p.OneTimePreKeys {
		if _, err := tx.Exec(`
			INSERT INTO im_e2e_one_time_pre_keys (uid, key_id, pre_key)
			VALUES ($1, $2, $3)
			ON CONFLICT (uid, key_id) DO NOTHING
		`, uid, otpk.KeyId, otpk.PreKey); err != nil {
			return fmt.Errorf("insert one-time prekey %d: %w", otpk.KeyId, err)
		}
	}

	return tx.Commit()
}

// GetE2eIdentity returns (nil, nil) if uid has never published a bundle —
// that is a legitimate, common state (most accounts have never opened an
// encrypted chat) and must be distinguishable from a real DB failure so
// handleE2eFetchBundle can answer Found:false instead of surfacing an
// internal error for it.
func (s *Store) GetE2eIdentity(uid string) (*E2eIdentityRow, error) {
	var r E2eIdentityRow
	err := s.db.QueryRow(`
		SELECT device_id, registration_id, identity_key,
			signed_pre_key_id, signed_pre_key, signed_pre_key_signature,
			kyber_pre_key_id, kyber_pre_key, kyber_pre_key_signature
		FROM im_e2e_identities WHERE uid = $1
	`, uid).Scan(&r.DeviceID, &r.RegistrationID, &r.IdentityKey,
		&r.SignedPreKeyID, &r.SignedPreKey, &r.SignedPreKeySignature,
		&r.KyberPreKeyID, &r.KyberPreKey, &r.KyberPreKeySignature)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ConsumeOneE2eOneTimePreKey atomically pops (marks consumed and returns)
// one still-unused one-time prekey for uid, or found=false if the pool is
// empty. FOR UPDATE SKIP LOCKED — not a plain SELECT-then-UPDATE — is what
// makes this safe under concurrency: two people fetching this account's
// bundle at the same moment must never be handed the same one-time key, and
// SKIP LOCKED means the second concurrent caller instantly moves on to the
// next available row instead of blocking behind the first caller's
// still-open transaction.
func (s *Store) ConsumeOneE2eOneTimePreKey(uid string) (keyID int, preKey string, found bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, "", false, err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRow(`
		SELECT id, key_id, pre_key FROM im_e2e_one_time_pre_keys
		WHERE uid = $1 AND consumed = false
		ORDER BY id ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, uid).Scan(&id, &keyID, &preKey)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	if _, err := tx.Exec(`UPDATE im_e2e_one_time_pre_keys SET consumed = true WHERE id = $1`, id); err != nil {
		return 0, "", false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, "", false, err
	}
	return keyID, preKey, true, nil
}

// CountUnconsumedE2eOneTimePreKeys reports how many one-time prekeys uid
// still has sitting in the pool unconsumed — a plain read, no locking needed
// (unlike ConsumeOneE2eOneTimePreKey, nothing here is handed out or mutated).
// Used by e2e_otpk_count so the client can decide whether a fresh top-up
// batch is actually needed before publishing one.
func (s *Store) CountUnconsumedE2eOneTimePreKeys(uid string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM im_e2e_one_time_pre_keys WHERE uid = $1 AND consumed = false`, uid).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// SetGroupEncrypted flips a group topic's encrypted flag on. Deliberately
// has no "off" counterpart — see ActionE2eSetGroupEncrypted's doc comment.
func (s *Store) SetGroupEncrypted(topic string) error {
	_, err := s.db.Exec(`UPDATE im_topics SET encrypted = true, updated_at = NOW() WHERE topic = $1 AND is_group = true`, topic)
	return err
}

// IsGroupEncrypted reports a group topic's current encrypted flag.
// err == sql.ErrNoRows style "topic doesn't exist" surfaces as (false, err)
// like every other single-row lookup in this file — callers fail closed on
// error rather than assuming an unencrypted default for a topic they
// couldn't actually verify.
func (s *Store) IsGroupEncrypted(topic string) (bool, error) {
	var encrypted bool
	err := s.db.QueryRow(`SELECT encrypted FROM im_topics WHERE topic = $1`, topic).Scan(&encrypted)
	if err != nil {
		return false, err
	}
	return encrypted, nil
}

// AreCommonEncryptedGroupMembers reports whether uidA and uidB currently
// share membership in at least one ENCRYPTED group — see e2ePeerAllowed's
// doc comment for why this is a valid E2EE trust boundary on its own,
// independent of whether the two are 1:1 friends.
func (s *Store) AreCommonEncryptedGroupMembers(uidA, uidB string) (bool, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM im_topic_members m1
		JOIN im_topic_members m2 ON m1.topic = m2.topic
		JOIN im_topics t ON t.topic = m1.topic
		WHERE m1.uid = $1 AND m2.uid = $2 AND t.is_group = true AND t.encrypted = true
	`, uidA, uidB).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// E2eEnvelope is one queued-for-offline-delivery encrypted payload — see
// im_e2e_envelopes' table comment for the retention story.
type E2eEnvelope struct {
	ID       int64
	Topic    string
	FromUID  string
	Kind     string
	Payload  string
	MsgType  int
}

// EnqueueE2eEnvelope persists one encrypted payload for a currently-offline
// recipient. handleE2eSend only ever calls this on the offline branch — an
// online recipient is handed the payload directly over their live socket and
// this table is never touched for them at all (see that handler).
func (s *Store) EnqueueE2eEnvelope(topic, toUID, fromUID, kind, payload string, msgType int, ttl time.Duration) error {
	_, err := s.db.Exec(`
		INSERT INTO im_e2e_envelopes (topic, to_uid, from_uid, kind, payload, msg_type, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW() + $7::interval)
	`, topic, toUID, fromUID, kind, payload, msgType, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	return err
}

// PopE2eEnvelopesForUID atomically returns and deletes every envelope queued
// for uid, oldest first, capped at 500 the same way GetPendingClears is —
// a login is not the place to let a pathological backlog stall the ack.
// DELETE...RETURNING makes "fetch" and "consume" a single round trip so a
// client that reconnects twice in quick succession can never be handed the
// same envelope twice.
func (s *Store) PopE2eEnvelopesForUID(uid string) ([]E2eEnvelope, error) {
	rows, err := s.db.Query(`
		DELETE FROM im_e2e_envelopes WHERE id IN (
			SELECT id FROM im_e2e_envelopes WHERE to_uid = $1 ORDER BY id ASC LIMIT 500
		)
		RETURNING id, topic, from_uid, kind, payload, msg_type
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []E2eEnvelope
	for rows.Next() {
		var e E2eEnvelope
		if err := rows.Scan(&e.ID, &e.Topic, &e.FromUID, &e.Kind, &e.Payload, &e.MsgType); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// ReapExpiredE2eEnvelopes deletes envelopes past expires_at regardless of
// delivery status — an encrypted message nobody was ever online to receive
// expires the same way an unanswered push notification would (see the
// table's doc comment), it does not accumulate as a permanent server-side
// copy.
func (s *Store) ReapExpiredE2eEnvelopes() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM im_e2e_envelopes WHERE expires_at < NOW()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// StartE2eEnvelopeReaper mirrors StartPendingClearsReaper's shape exactly —
// same hourly cadence, same "log and continue" error handling — for the
// same reason: a transient DB hiccup on one tick must not take the whole
// background sweep down.
func (s *Store) StartE2eEnvelopeReaper() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		n, err := s.ReapExpiredE2eEnvelopes()
		if err != nil {
			logf("reapE2eEnvelopes: %v", err)
			continue
		}
		if n > 0 {
			logf("reapE2eEnvelopes: dropped %d expired e2e envelope(s)", n)
		}
	}
}

func init() {
	// Ensure sha256 is imported
	var _ = sha256.Sum256
}
