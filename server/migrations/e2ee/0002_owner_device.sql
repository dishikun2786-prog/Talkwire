-- The connection device that published the identity (task 1.12). E2EE stays
-- single-device in v1: envelopes are relayed to, queued for and replayed to
-- this device only, so another device of the same user (one without the
-- keys, such as the web client) never takes them. Identities published before
-- this migration belong to the device "".
ALTER TABLE im_e2e_identities ADD COLUMN IF NOT EXISTS owner_device VARCHAR(64) NOT NULL DEFAULT '';
