// Store: push tokens.
//
// The table keeps its historical name, im_fcm_tokens, although it holds the
// tokens of every provider: renaming it would break the upgrade of existing
// databases. Core migration 0002 added the provider column; rows from before
// it are FCM tokens.

package main

import (
	"database/sql"

	"example.com/imkit/server/push"
)

// GetPushTokensBatch resolves push tokens for many uids in one round trip.
//
// A per-uid lookup is fine for a 1:1 message, but the offline-push path for a
// group message called it once per offline member: a 200-person group with
// most members offline turned one incoming message into ~200 sequential
// queries, each taking a pooled connection, on the hot path of every single
// group send. uids with no registered device are simply absent from the map.
func (s *Store) GetPushTokensBatch(uids []string) (map[string][]deviceToken, error) {
	out := make(map[string][]deviceToken, len(uids))
	if len(uids) == 0 {
		return out, nil
	}
	ph, args := inPlaceholders(uids)
	rows, err := s.db.Query(`SELECT uid, provider, token, device_id FROM im_fcm_tokens WHERE uid IN (`+ph+`)`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid string
		var t deviceToken
		if err := rows.Scan(&uid, &t.Provider, &t.Value, &t.Device); err != nil {
			return out, err
		}
		out[uid] = append(out[uid], t)
	}
	return out, rows.Err()
}

// deviceToken is a push token and the device that registered it ("" when the
// login named none).
type deviceToken struct {
	push.Token
	Device string
}

// SavePushToken registers a device token for push notifications, reassigning
// it away from whatever uid held it before. token is the primary key (see the
// core baseline migration), so this ON CONFLICT clause is what actually
// enforces "a device token belongs to one logged-in account at a time" -- the
// previous owner's row is overwritten, not left to accumulate alongside this
// one.
//
// device is the device that registered it (task 1.12). A named device keeps
// one token per provider: registering a new one (a rotated FCM token, a new
// Web Push subscription) drops its old one, so the device is not notified
// twice. Tokens of the device "" are never dropped this way: logins that name
// no device cannot be told apart.
func (s *Store) SavePushToken(uid, provider, token, device string) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT INTO im_fcm_tokens (uid, token, provider, device_id) VALUES ($1, $2, $3, $4)
			ON CONFLICT (token) DO UPDATE SET uid = EXCLUDED.uid, provider = EXCLUDED.provider,
				device_id = EXCLUDED.device_id, updated_at = NOW()
		`, uid, token, provider, device); err != nil {
			return err
		}
		if device == "" {
			return nil
		}
		_, err := tx.Exec(`
			DELETE FROM im_fcm_tokens
			WHERE uid = $1 AND device_id = $2 AND provider = $3 AND token <> $4
		`, uid, device, provider, token)
		return err
	})
}

// DeletePushToken unregisters one device token for one uid, scoped to that
// uid so a stale unreg request racing a fresher SavePushToken for a
// different uid on the same token can't delete the wrong owner's row.
// Called on client-initiated logout (unreg_token) and is a no-op if the
// token was already reassigned or removed.
func (s *Store) DeletePushToken(uid, token string) error {
	_, err := s.db.Exec(`DELETE FROM im_fcm_tokens WHERE uid = $1 AND token = $2`, uid, token)
	return err
}

// DeletePushTokenByValue removes a device token regardless of which uid
// currently owns it. Used when the provider itself reports the token as gone
// (app uninstalled, token rotated, subscription expired) -- at that point the
// token is dead everywhere, not just for the uid we happened to be pushing to.
func (s *Store) DeletePushTokenByValue(token string) error {
	_, err := s.db.Exec(`DELETE FROM im_fcm_tokens WHERE token = $1`, token)
	return err
}
