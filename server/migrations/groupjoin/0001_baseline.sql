-- Baseline: the schema of the reference server (33e23250), generated from
-- its Store.InitTables. Every statement is idempotent, so this also runs
-- against a database the reference server already created.

-- im_group_invite_tokens holds at most one active scan-to-join QR token
-- per group (PK = topic, upsert-on-mint == "refresh"). Unlike
-- im_group_settings this is not a config row a group may go without --
-- GetActiveGroupQrToken/MintGroupQrToken create one on first request.
-- Same F-21 caveat as im_group_settings applies: the CASCADE only fires
-- if the im_topics row itself is deleted, which DisbandGroup
-- deliberately never does -- DisbandGroup below has an explicit DELETE
-- for this table so a disbanded group's token can't outlive it.
CREATE TABLE IF NOT EXISTS im_group_invite_tokens (
			topic      VARCHAR(64)  PRIMARY KEY REFERENCES im_topics(topic) ON DELETE CASCADE,
			token      VARCHAR(64)  NOT NULL,
			created_by VARCHAR(64)  NOT NULL,
			created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ  NOT NULL
		);

CREATE UNIQUE INDEX IF NOT EXISTS idx_im_group_invite_tokens_token ON im_group_invite_tokens(token);

-- im_group_join_requests records a scan-to-join request awaiting
-- owner/admin review when im_group_settings.require_join_approval is
-- on. The partial unique index blocks a second concurrent "pending"
-- row for the same (topic, requester) -- a doubled tap or retry
-- collapses onto the request already awaiting review instead of
-- spamming the queue; a fresh request after a prior one was
-- approved/rejected is allowed (this index only sees status='pending'
-- rows).
CREATE TABLE IF NOT EXISTS im_group_join_requests (
			id            BIGSERIAL    PRIMARY KEY,
			topic         VARCHAR(64)  NOT NULL,
			requester_uid VARCHAR(64)  NOT NULL,
			message       VARCHAR(120) NOT NULL DEFAULT '',
			status        VARCHAR(16)  NOT NULL DEFAULT 'pending',
			reviewed_by   VARCHAR(64)  NOT NULL DEFAULT '',
			reviewed_at   TIMESTAMPTZ,
			created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		);

CREATE INDEX IF NOT EXISTS idx_im_group_join_requests_topic ON im_group_join_requests(topic, status);

CREATE UNIQUE INDEX IF NOT EXISTS idx_im_group_join_requests_pending ON im_group_join_requests(topic, requester_uid) WHERE status = 'pending';
