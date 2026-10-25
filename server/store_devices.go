// Store: the devices users log in from, and which devices already have a
// queued row (task 1.12).

package main

import (
	"database/sql"
	"strconv"
	"time"
)

// deviceActiveWindow is how long after it was last seen a device still counts
// as one of the user's devices: the queues' own expiry.
const deviceActiveWindow = pendingQueueMaxAge

// Queue names in im_pending_acks.
const (
	queueClear      = "clear"
	queueGroupEvent = "group_event"
)

// EnsureDevice records that uid was seen on device now.
func (s *Store) EnsureDevice(uid, device string) error {
	_, err := s.db.Exec(`
		INSERT INTO im_devices (uid, device_id) VALUES ($1, $2)
		ON CONFLICT (uid, device_id) DO UPDATE SET last_seen_at = NOW()
	`, uid, device)
	return err
}

// ReapStaleDevices forgets devices not seen within deviceActiveWindow of now:
// they no longer count for the queues, and a client that names a new device
// at every login would otherwise grow im_devices without end. A device
// connected for longer than the window is recorded again when it
// disconnects.
func (s *Store) ReapStaleDevices(now time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM im_devices WHERE last_seen_at < $1`, now.Add(-deviceActiveWindow))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// KnownDevices returns, per uid, the devices seen in the last 30 days.
func (s *Store) KnownDevices(uids []string) (map[string][]string, error) {
	out := make(map[string][]string, len(uids))
	if len(uids) == 0 {
		return out, nil
	}
	ph, args := inPlaceholders(uids)
	args = append(args, time.Now().Add(-deviceActiveWindow))
	rows, err := s.db.Query(`
		SELECT uid, device_id FROM im_devices
		WHERE uid IN (`+ph+`) AND last_seen_at > $`+strconv.Itoa(len(args))+`
		ORDER BY uid, device_id
	`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid, device string
		if err := rows.Scan(&uid, &device); err != nil {
			return out, err
		}
		out[uid] = append(out[uid], device)
	}
	return out, rows.Err()
}

// withTx runs fn in a transaction, committing when it returns nil.
func (s *Store) withTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// resetAcks marks a queued row as had by exactly the given devices: a row a
// newer change replaced is new to every device that has not just got it.
func resetAcks(tx *sql.Tx, queue string, id int64, delivered []string) error {
	if _, err := tx.Exec(`DELETE FROM im_pending_acks WHERE queue = $1 AND item_id = $2`, queue, id); err != nil {
		return err
	}
	for _, d := range delivered {
		if _, err := tx.Exec(`
			INSERT INTO im_pending_acks (queue, item_id, device_id) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING
		`, queue, id, d); err != nil {
			return err
		}
	}
	return nil
}

// ackPending records that device has the queued row id of uid, and deletes
// the row once every device seen in the last 30 days has it. table is one of
// the two queue tables. The row is locked first, so two devices acknowledging
// at once cannot both miss the other's acknowledgement.
func (s *Store) ackPending(queue, table string, id int64, uid, device string) error {
	return s.withTx(func(tx *sql.Tx) error {
		var owner string
		err := tx.QueryRow(`SELECT uid FROM `+table+` WHERE id = $1 AND uid = $2 FOR UPDATE`, id, uid).Scan(&owner)
		if err == sql.ErrNoRows {
			return nil // already gone, or not this user's
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO im_pending_acks (queue, item_id, device_id) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING
		`, queue, id, device); err != nil {
			return err
		}
		res, err := tx.Exec(`
			DELETE FROM `+table+` t WHERE t.id = $1 AND NOT EXISTS (
				SELECT 1 FROM im_devices d
				WHERE d.uid = t.uid AND d.last_seen_at > $3 AND NOT EXISTS (
					SELECT 1 FROM im_pending_acks a
					WHERE a.queue = $2 AND a.item_id = t.id AND a.device_id = d.device_id))
		`, id, queue, time.Now().Add(-deviceActiveWindow))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			_, err = tx.Exec(`DELETE FROM im_pending_acks WHERE queue = $1 AND item_id = $2`, queue, id)
		}
		return err
	})
}

// reapOrphanAcks drops acknowledgements of queued rows that no longer exist.
func (s *Store) reapOrphanAcks(queue, table string) error {
	_, err := s.db.Exec(`
		DELETE FROM im_pending_acks a WHERE a.queue = $1
		AND NOT EXISTS (SELECT 1 FROM `+table+` t WHERE t.id = a.item_id)
	`, queue)
	return err
}
