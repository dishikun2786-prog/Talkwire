-- Several devices per user (task 1.12).
--
-- im_devices records each device a user has logged in from. A change made
-- while some of a user's devices are away (a conversation clear, a group
-- membership event) is queued for them; devices not seen for 30 days no
-- longer count, matching the queues' own 30-day expiry. A login that names
-- no device is the device "".
CREATE TABLE IF NOT EXISTS im_devices (
	uid          VARCHAR(64) NOT NULL,
	device_id    VARCHAR(64) NOT NULL,
	last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (uid, device_id)
);

-- im_pending_acks records which devices already have a queued row of
-- im_pending_clears ("clear") or im_pending_group_events ("group_event"),
-- live or replayed and acknowledged. A queued row stays pending for every
-- other device; it is deleted once every device seen in the last 30 days
-- has it. With one device per user this deletes the row at its first
-- acknowledgement, as before.
CREATE TABLE IF NOT EXISTS im_pending_acks (
	queue     VARCHAR(16) NOT NULL,
	item_id   BIGINT NOT NULL,
	device_id VARCHAR(64) NOT NULL,
	PRIMARY KEY (queue, item_id, device_id)
);

-- Push tokens name the device that registered them, so a user's devices that
-- are away still get a notification while another of their devices is
-- connected, and a device that replaces its token keeps only the new one.
-- Rows registered before this migration, and tokens registered by a login
-- that named no device, have device "".
ALTER TABLE im_fcm_tokens ADD COLUMN IF NOT EXISTS device_id VARCHAR(64) NOT NULL DEFAULT '';
