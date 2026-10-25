-- Baseline: the schema of the reference server (33e23250), generated from
-- its Store.InitTables. Every statement is idempotent, so this also runs
-- against a database the reference server already created.

-- -- E2EE --
--
-- im_e2e_identities holds each account's currently-published X3DH+PQXDH
-- prekey bundle. Keyed by uid (im-go's internal identifier), matching
-- every other table in this file (im_topic_members.uid,
-- im_messages.from_uid, ...) rather than the main server's app_user_id
-- UUID — resolving one from the other is an extra DB round trip these
-- handlers have no other reason to make, since client.uid is already
-- on hand for every authenticated action.
--
-- PK on uid, not (uid, device_id): this first implementation
-- deliberately supports exactly one "current" encryption-capable
-- device per account rather than fanning a session out to every
-- device on the account the way Signal's own multi-device linking
-- does — publishing again from a second device silently supersedes
-- the first. That's a real, documented scope reduction, not an
-- oversight: encrypted threads are explicitly device-pinned/
-- non-synced already (see the design doc), so a user who switches
-- devices restarts encrypted conversations rather than migrating them.
-- All key-material columns are TEXT holding base64 — every one of them
-- is public key data or a signature over public key data, never a
-- secret, which is what makes it safe for this table to be ordinarily
-- readable server-side at all.
CREATE TABLE IF NOT EXISTS im_e2e_identities (
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
		);

-- im_e2e_one_time_pre_keys is a per-uid pool of single-use Curve25519
-- prekeys, consumed exactly once by ConsumeOneE2eOneTimePreKey's
-- FOR UPDATE SKIP LOCKED pop so two people fetching this account's
-- bundle concurrently can never be handed the same one-time key.
CREATE TABLE IF NOT EXISTS im_e2e_one_time_pre_keys (
			id           BIGSERIAL PRIMARY KEY,
			uid          VARCHAR(64) NOT NULL,
			key_id       INT NOT NULL,
			pre_key      TEXT NOT NULL,
			consumed     BOOLEAN NOT NULL DEFAULT false,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(uid, key_id)
		);

CREATE INDEX IF NOT EXISTS idx_im_e2e_otpk_available ON im_e2e_one_time_pre_keys(uid, id) WHERE consumed = false;

-- im_e2e_envelopes is a bounded-TTL relay queue for encrypted content
-- aimed at a currently-offline recipient — see ActionE2eSend's doc
-- comment in protocol.go for why this, and not im_messages, is where
-- it lives. Rows are deleted the moment they're delivered
-- (PopE2eEnvelopesForUID) and reaped unconditionally past expires_at
-- regardless of delivery (StartE2eEnvelopeReaper) — an encrypted
-- message that nobody was ever online to receive expires the same way
-- an unanswered push notification would, rather than accumulating
-- forever as a permanent server-side copy of content the product
-- promises never becomes one.
CREATE TABLE IF NOT EXISTS im_e2e_envelopes (
			id          BIGSERIAL PRIMARY KEY,
			topic       VARCHAR(128) NOT NULL,
			to_uid      VARCHAR(64) NOT NULL,
			from_uid    VARCHAR(64) NOT NULL,
			kind        VARCHAR(24) NOT NULL,
			payload     TEXT NOT NULL DEFAULT '',
			msg_type    INT NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at  TIMESTAMPTZ NOT NULL
		);

CREATE INDEX IF NOT EXISTS idx_im_e2e_envelopes_to ON im_e2e_envelopes(to_uid, id);

-- StartE2eEnvelopeReaper's hourly DELETE filters on expires_at alone —
-- without this it degrades to a full-table scan every hour as the
-- queue grows under sustained offline-recipient traffic (TTL is up to
-- e2ee.envelope_ttl_hours, default 14 days), unlike its sibling
-- PopE2eEnvelopesForUID which is already bounded by idx_im_e2e_envelopes_to.
CREATE INDEX IF NOT EXISTS idx_im_e2e_envelopes_expires ON im_e2e_envelopes(expires_at);
