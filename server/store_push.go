// Store: push tokens.
//
// The table keeps its historical name, im_fcm_tokens, although it holds the
// tokens of every provider: renaming it would break the upgrade of existing
// databases. Core migration 0002 added the provider column; rows from before
// it are FCM tokens.

package main

import "example.com/imkit/server/push"

// GetPushTokensBatch resolves push tokens for many uids in one round trip.
//
// A per-uid lookup is fine for a 1:1 message, but the offline-push path for a
// group message called it once per offline member: a 200-person group with
// most members offline turned one incoming message into ~200 sequential
// queries, each taking a pooled connection, on the hot path of every single
// group send. uids with no registered device are simply absent from the map.
func (s *Store) GetPushTokensBatch(uids []string) (map[string][]push.Token, error) {
	out := make(map[string][]push.Token, len(uids))
	if len(uids) == 0 {
		return out, nil
	}
	ph, args := inPlaceholders(uids)
	rows, err := s.db.Query(`SELECT uid, provider, token FROM im_fcm_tokens WHERE uid IN (`+ph+`)`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid string
		var t push.Token
		if err := rows.Scan(&uid, &t.Provider, &t.Value); err != nil {
			return out, err
		}
		out[uid] = append(out[uid], t)
	}
	return out, rows.Err()
}

// SavePushToken registers a device token for push notifications, reassigning
// it away from whatever uid held it before. token is the primary key (see the
// core baseline migration), so this ON CONFLICT clause is what actually
// enforces "a device token belongs to one logged-in account at a time" -- the
// previous owner's row is overwritten, not left to accumulate alongside this
// one.
func (s *Store) SavePushToken(uid, provider, token string) error {
	_, err := s.db.Exec(`
		INSERT INTO im_fcm_tokens (uid, token, provider) VALUES ($1, $2, $3)
		ON CONFLICT (token) DO UPDATE SET uid = EXCLUDED.uid, provider = EXCLUDED.provider, updated_at = NOW()
	`, uid, token, provider)
	return err
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
