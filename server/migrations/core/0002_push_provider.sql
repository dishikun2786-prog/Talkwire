-- Push tokens are no longer FCM-only: each row names its provider ("fcm",
-- "apns" or "webpush"). Rows registered before this migration are FCM tokens,
-- the only kind older clients could register.
ALTER TABLE im_fcm_tokens ADD COLUMN IF NOT EXISTS provider VARCHAR(16) NOT NULL DEFAULT 'fcm';
