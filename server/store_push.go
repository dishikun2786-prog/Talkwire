// Store: push tokens.

package main

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
