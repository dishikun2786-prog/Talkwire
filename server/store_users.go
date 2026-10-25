// Store: users, uid mapping and last-seen.

package main

import (
	"database/sql"
	"fmt"
	"strings"
)

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
