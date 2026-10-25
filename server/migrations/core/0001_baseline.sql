-- Baseline: the schema of the reference server (33e23250), generated from
-- its Store.InitTables. Every statement is idempotent, so this also runs
-- against a database the reference server already created.

CREATE TABLE IF NOT EXISTS im_users (
			app_user_id VARCHAR(64) PRIMARY KEY,
			im_user_id VARCHAR(64) UNIQUE NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

CREATE TABLE IF NOT EXISTS im_messages (
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
		);

CREATE INDEX IF NOT EXISTS idx_im_messages_topic ON im_messages(topic, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_im_messages_topic_seq ON im_messages(topic, seq DESC);

CREATE TABLE IF NOT EXISTS im_topics (
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
		);

CREATE TABLE IF NOT EXISTS im_topic_members (
			topic VARCHAR(128) NOT NULL,
			uid VARCHAR(64) NOT NULL,
			role VARCHAR(20) NOT NULL DEFAULT 'member',
			joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(topic, uid)
		);

CREATE INDEX IF NOT EXISTS idx_im_topic_members_uid ON im_topic_members(uid);

-- uid is intentionally NOT part of the primary key -- see the migration
-- immediately below for why a lone device token being claimable by more
-- than one uid at once was a real cross-account push-notification leak,
-- not just a theoretical one.
CREATE TABLE IF NOT EXISTS im_fcm_tokens (
			uid VARCHAR(64) NOT NULL,
			token TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(uid, token)
		);

-- Migrates the primary key from (uid, token) to (token) alone.
--
-- A physical device only ever runs one logged-in account's push
-- notifications at a time, but the old (uid, token) key let the SAME
-- FCM token accumulate one row per account that ever logged into this
-- app on that device -- nothing ever deleted the old account's row on
-- logout (there was no unreg path at all) or on switching accounts.
-- pushOfflineRecipients selects tokens purely by uid with no idea a
-- token might currently be live under a different account, so the
-- moment account B logs in on a device account A used before, A's
-- still-offline messages start arriving in B's system notification
-- tray with A's sender name and message preview -- a real content
-- leak between two people who share (or resell) a phone.
--
-- Making token the sole primary key turns SaveFCMToken's upsert (see
-- its doc comment) into "claiming a token retires whoever held it
-- before", which is exactly the single-current-owner invariant this
-- table needs and takes effect immediately for every already-deployed
-- client, without waiting on the companion end-of-fix that has the
-- client explicitly unreg on logout.
--
-- Guarded so the (moderately expensive, table-locking) dedupe+rekey
-- only ever runs once: on a fresh table conkey already matches, so the
-- IF NOT EXISTS check short-circuits before either statement runs.
DO $$
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
		END $$;

-- im_group_settings is a 1:1 extension of im_topics (group forward/AI-QA
-- config), not a separate group table.
--
-- ⚠️ 更正（F-21）：这里原来写着"ON DELETE CASCADE 意味着 DisbandGroup
-- 现有的成员清理就够了，不需要额外代码避免孤儿设置行"——**这句话与事实
-- 相反**。CASCADE 挂在 im_topics(topic) 上，只有删除 im_topics 那一行才
-- 会级联；而 DisbandGroup 刻意保留 im_topics 行（保住历史消息），只删
-- im_topic_members，级联因此永远不会触发。解散群后设置行原样留在库里，
-- 万一同一个 topic 字符串被复用（RandString 碰撞）或群被人工恢复，上一
-- 个群的 AI 知识库配置会直接生效。现由 DisbandGroup 显式 DELETE。
CREATE TABLE IF NOT EXISTS im_group_settings (
			topic VARCHAR(64) PRIMARY KEY REFERENCES im_topics(topic) ON DELETE CASCADE,
			ai_qa_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			ai_kb_ids JSONB NOT NULL DEFAULT '[]',
			ai_doc_ids JSONB NOT NULL DEFAULT '[]',
			ai_daily_quota INT NOT NULL DEFAULT 50,
			ai_cooldown_sec INT NOT NULL DEFAULT 5,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_by VARCHAR(64) NOT NULL
		);

-- im_pending_clears is the per-member offline-delivery queue for
-- topic_cleared: broadcastTopicCleared writes one row here for each
-- member who was offline at the moment a conversation was cleared,
-- instead of silently dropping the event. UNIQUE(topic, uid) collapses
-- repeated clears of the same topic while a member stays offline down
-- to a single row holding only the latest clear_version -- the same
-- "latest-wins, not a log" shape as im_topics.clear_version itself.
CREATE TABLE IF NOT EXISTS im_pending_clears (
			id BIGSERIAL PRIMARY KEY,
			topic VARCHAR(128) NOT NULL,
			uid VARCHAR(64) NOT NULL,
			wire_topic VARCHAR(128) NOT NULL,
			clear_version INT NOT NULL,
			clear_before_seq INT NOT NULL,
			from_uid VARCHAR(64) NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(topic, uid)
		);

CREATE INDEX IF NOT EXISTS idx_im_pending_clears_uid ON im_pending_clears(uid);

-- im_pending_group_events is the same offline-delivery queue shape as
-- im_pending_clears, one level up: grp_sync ("你的群成员/角色变了，去刷新")
-- 和 grp_disband ("这个群没了") 此前只对当时在线的成员广播，离线成员
-- 什么都收不到，只能等自己下次主动 getGroupTopics() 才自愈——被拉进群
-- 的人在重启 App 之前根本看不到这个群。
--
-- UNIQUE(topic, uid, kind) 让"同一个成员离线期间被反复改角色"收敛成一
-- 行：这些事件本身只是"去刷新"的提示，不携带增量，重复投递没有意义。
-- kind 参与唯一键是因为 grp_sync 与 grp_disband 语义互斥但都可能排队，
-- 不能互相覆盖（先改角色后解散，两条都得投递，且顺序由 id 保证）。
CREATE TABLE IF NOT EXISTS im_pending_group_events (
			id BIGSERIAL PRIMARY KEY,
			topic VARCHAR(128) NOT NULL,
			uid VARCHAR(64) NOT NULL,
			kind VARCHAR(24) NOT NULL,
			group_name VARCHAR(255) NOT NULL DEFAULT '',
			from_uid VARCHAR(64) NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(topic, uid, kind)
		);

CREATE INDEX IF NOT EXISTS idx_im_pending_group_events_uid ON im_pending_group_events(uid);

-- im_read_cursors is a per-(topic,uid) read cursor, not a per-message
-- reader row: a group can have up to 500 members and a history page up
-- to 200 messages, and "who has read message X" needs to stay
-- O(members) regardless of how many messages are on screen. A message
-- with seq S counts as read by uid U iff cursor(topic,U).last_read_seq
-- >= S -- reader sets are computed at read time from this table, never
-- stored per message, which is also why a recalled message needs no
-- cleanup here: its seq just stops being queried once the message row
-- is gone.
CREATE TABLE IF NOT EXISTS im_read_cursors (
			topic          VARCHAR(128) NOT NULL,
			uid            VARCHAR(64) NOT NULL,
			last_read_seq  INT NOT NULL DEFAULT 0,
			updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (topic, uid)
		);

CREATE INDEX IF NOT EXISTS idx_im_read_cursors_topic ON im_read_cursors(topic);

-- Existing installations created im_topics/im_messages before this
-- migration existed. Keep it idempotent because IM tables are initialized
-- by this standalone process instead of Prisma.
ALTER TABLE im_topics ADD COLUMN IF NOT EXISTS clear_version INT NOT NULL DEFAULT 0;

ALTER TABLE im_topics ADD COLUMN IF NOT EXISTS clear_before_seq INT NOT NULL DEFAULT 0;

ALTER TABLE im_topics ADD COLUMN IF NOT EXISTS cleared_at TIMESTAMPTZ;

-- encrypted is group-only in practice (see ActionE2eSetGroupEncrypted):
-- a 1:1 encrypted thread never shares a topic id with its plain
-- counterpart to begin with (it lives under a separate e2e_ topic
-- namespace that never appears in im_topics at all), so there is
-- nothing for a boolean flag to disambiguate there. Defaults false —
-- existing groups are unaffected until their owner opts in.
ALTER TABLE im_topics ADD COLUMN IF NOT EXISTS encrypted BOOLEAN NOT NULL DEFAULT FALSE;

-- Presence itself stays in memory — after a restart every socket really
-- is dead, so "everyone offline" is the correct answer, not state to
-- recover. last_seen_at exists only so the UI can say "最近在线 X 分钟前"
-- instead of asserting a hard offline bit, which is far more tolerant of
-- the transient churn cross-border clients live with.
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;

-- require_join_approval gates whether group_join (scan-to-join) admits
-- immediately or files a im_group_join_requests row instead. Defaults
-- FALSE so shipping this column changes nothing for any existing group
-- until its owner explicitly opts in (hardcode-to-config-default rule).
ALTER TABLE im_group_settings ADD COLUMN IF NOT EXISTS require_join_approval BOOLEAN NOT NULL DEFAULT FALSE;

-- Forward provenance + AI-virtual-sender tag. forward_from_msg_id
-- intentionally has no FK: it points at a (topic, seq) pair in a
-- *different* topic than the row it's attached to, and im_messages'
-- only unique key is the composite (topic, seq) — not something a
-- single-column FK can reference.
ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS forward_from_msg_id BIGINT DEFAULT NULL;

ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS forward_from_topic VARCHAR(64) DEFAULT NULL;

ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS forward_chain_depth SMALLINT NOT NULL DEFAULT 0;

ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS sender_kind VARCHAR(16) NOT NULL DEFAULT 'user';

-- client_msg_id backs SaveMessageFullIdempotent's dedup -- see its doc
-- comment. Nullable and only unique when present: forwards, AI
-- replies, and any client that hasn't started sending one yet must
-- keep working unconstrained. CREATE UNIQUE INDEX IF NOT EXISTS is
-- naturally idempotent (unlike ADD CONSTRAINT, no guard needed).
ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS client_msg_id VARCHAR(64) DEFAULT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_im_messages_topic_client_msg_id ON im_messages(topic, client_msg_id) WHERE client_msg_id IS NOT NULL;

-- recalled_at backs SoftDeleteMessage. A hard DELETE frees the (topic,
-- seq) pair, and GetNextSeq only ever hands out last_seq+1 -- so the
-- hole a recall leaves behind is permanent and indistinguishable, to
-- a client doing gap detection over its local seq series, from a
-- message it simply never received. It then asks for that range
-- forever and never gets an answer. Keeping the row as a tombstone
-- preserves the seq series; GetMessages/GetMessagesAfter filter it
-- out so the content really is gone for every reader.
ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS recalled_at TIMESTAMPTZ DEFAULT NULL;
