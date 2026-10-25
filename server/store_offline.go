// Store: offline queues for conversation clears and group events.

package main

import (
	"database/sql"
	"fmt"
	"time"
)

// PendingClear is one durable "you missed this clear while offline" tombstone
// row, holding everything deliverPendingClears needs to replay it exactly as
// though it had been broadcastTopicCleared to an online client.
type PendingClear struct {
	ID             int64
	WireTopic      string
	ClearVersion   int
	ClearBeforeSeq int
	FromUID        string
}

// EnqueuePendingClear upserts a per-member pending-clear tombstone for a
// member who was offline at the moment broadcastTopicCleared ran. The
// UNIQUE(topic, uid) constraint plus the WHERE clause below collapse "the
// same topic cleared more than once while this member stays offline" down to
// a single row holding only the latest clearVersion -- mirroring how
// im_topics.clear_version itself is a single latest-wins value, not a log.
//
// delivered lists the user's devices that already have this clear (they got
// it live); the row is pending for the others (task 1.12).
func (s *Store) EnqueuePendingClear(topic, uid, wireTopic string, clearVersion, clearBeforeSeq int, fromUID string, delivered []string) error {
	return s.withTx(func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRow(`
			INSERT INTO im_pending_clears (topic, uid, wire_topic, clear_version, clear_before_seq, from_uid)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (topic, uid) DO UPDATE SET
				wire_topic = EXCLUDED.wire_topic,
				clear_version = EXCLUDED.clear_version,
				clear_before_seq = EXCLUDED.clear_before_seq,
				from_uid = EXCLUDED.from_uid,
				created_at = NOW()
			WHERE EXCLUDED.clear_version > im_pending_clears.clear_version
			RETURNING id
		`, topic, uid, wireTopic, clearVersion, clearBeforeSeq, fromUID).Scan(&id)
		if err == sql.ErrNoRows {
			return nil // a newer clear of this topic is already queued
		}
		if err != nil {
			return err
		}
		return resetAcks(tx, queueClear, id, delivered)
	})
}

// GetPendingClears returns every clear this uid still needs to be told about,
// oldest first. Capped so a pathological backlog can't stall a login.
//
// Only the clears device does not have yet (task 1.12).
func (s *Store) GetPendingClears(uid, device string) ([]PendingClear, error) {
	rows, err := s.db.Query(`
		SELECT c.id, c.wire_topic, c.clear_version, c.clear_before_seq, c.from_uid
		FROM im_pending_clears c
		WHERE c.uid = $1 AND NOT EXISTS (
			SELECT 1 FROM im_pending_acks a
			WHERE a.queue = 'clear' AND a.item_id = c.id AND a.device_id = $2)
		ORDER BY c.id ASC LIMIT 500
	`, uid, device)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingClear
	for rows.Next() {
		var p PendingClear
		if err := rows.Scan(&p.ID, &p.WireTopic, &p.ClearVersion, &p.ClearBeforeSeq, &p.FromUID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AckPendingClear deletes one delivered tombstone, scoped to uid so a client
// can never ack (and silently drop) another user's pending clear by guessing
// an id.
//
// The row is deleted once every device of the user has it (task 1.12); with
// one device, at its first acknowledgement as in the reference server.
func (s *Store) AckPendingClear(id int64, uid, device string) error {
	return s.ackPending(queueClear, "im_pending_clears", id, uid, device)
}

// pendingQueueMaxAge is how long an unacknowledged clear or group event is
// kept (red lines R7, R8). The reapers take the current time as a parameter so
// the expiry can be tested without waiting a month; created_at is TIMESTAMPTZ,
// so comparing it with a time from Go is not shifted by the session time zone.
const pendingQueueMaxAge = 30 * 24 * time.Hour

// ReapExpiredPendingClears drops tombstones nobody has ack'd within
// pendingQueueMaxAge of now --
// the backstop for clients too old to understand ack_topic_cleared. Safe to
// drop unconditionally: any client that eventually reconnects and re-fetches
// that topic still gets the authoritative clear_version/clear_before_seq from
// im_topics via handleGet, independent of this queue.
func (s *Store) ReapExpiredPendingClears(now time.Time) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM im_pending_clears WHERE created_at < $1`,
		now.Add(-pendingQueueMaxAge),
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, s.reapOrphanAcks(queueClear, "im_pending_clears")
}

// StartPendingClearsReaper runs ReapStaleDevices and ReapExpiredPendingClears
// on an hourly tick.
// Launch with `go` once from main.go, mirroring hub.gcForwardReqCache's
// launch pattern.
func (s *Store) StartPendingClearsReaper() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		if n, err := s.ReapStaleDevices(time.Now()); err != nil {
			logf("reapStaleDevices: %v", err)
		} else if n > 0 {
			logf("reapStaleDevices: forgot %d device(s) not seen for 30 days", n)
		}
		n, err := s.ReapExpiredPendingClears(time.Now())
		if err != nil {
			logf("reapPendingClears: %v", err)
			continue
		}
		if n > 0 {
			logf("reapPendingClears: dropped %d expired pending clear(s)", n)
		}
	}
}

// PendingGroupEvent is one queued "your group membership changed while you
// were offline" notice, holding everything deliverPendingGroupEvents needs
// to replay the push exactly as an online member would have received it.
type PendingGroupEvent struct {
	ID        int64
	Topic     string
	UID       string
	Kind      string
	GroupName string
	FromUID   string
}

// EnqueuePendingGroupEvent records a grp_sync / grp_disband notice for a
// member who was offline when it fired.
//
// These pushes used to be online-only: a member who was added to a group,
// had their role changed, or whose group was disbanded while disconnected
// received nothing at all, and only found out on their next unrelated full
// refresh -- in practice, after killing and reopening the app.
//
// ON CONFLICT collapses repeats rather than queueing a second row: the
// payload is a "go refresh" nudge with no delta in it, so N identical
// notices and one notice are worth exactly the same to the client. The
// refreshed created_at keeps the reaper from expiring a notice that is
// still being re-raised.
//
// delivered lists the user's devices that already have this event (task
// 1.12), as in EnqueuePendingClear.
func (s *Store) EnqueuePendingGroupEvent(topic, uid, kind, groupName, fromUID string, delivered []string) error {
	switch kind {
	case PendingGroupEventSync, PendingGroupEventDisband,
		PendingGroupEventJoinRequest, PendingGroupEventJoinApproved, PendingGroupEventJoinRejected:
	default:
		return fmt.Errorf("enqueue pending group event: unknown kind %q", kind)
	}
	return s.withTx(func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRow(`
			INSERT INTO im_pending_group_events (topic, uid, kind, group_name, from_uid)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (topic, uid, kind) DO UPDATE SET
				group_name = EXCLUDED.group_name,
				from_uid = EXCLUDED.from_uid,
				created_at = NOW()
			RETURNING id
		`, topic, uid, kind, groupName, fromUID).Scan(&id); err != nil {
			return err
		}
		return resetAcks(tx, queueGroupEvent, id, delivered)
	})
}

// GetPendingGroupEvents returns everything this uid still needs to be told
// about, oldest first so a grp_sync that preceded a grp_disband is replayed
// in that order. Capped so a pathological backlog cannot stall a login.
//
// Only the events device does not have yet (task 1.12).
func (s *Store) GetPendingGroupEvents(uid, device string) ([]PendingGroupEvent, error) {
	rows, err := s.db.Query(`
		SELECT e.id, e.topic, e.uid, e.kind, e.group_name, e.from_uid
		FROM im_pending_group_events e
		WHERE e.uid = $1 AND NOT EXISTS (
			SELECT 1 FROM im_pending_acks a
			WHERE a.queue = 'group_event' AND a.item_id = e.id AND a.device_id = $2)
		ORDER BY e.id ASC LIMIT 500
	`, uid, device)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingGroupEvent
	for rows.Next() {
		var e PendingGroupEvent
		if err := rows.Scan(&e.ID, &e.Topic, &e.UID, &e.Kind, &e.GroupName, &e.FromUID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AckPendingGroupEvent deletes one delivered notice, scoped to the uid that
// is acking it.
//
// ⚠️ 修复记录：这里原来没有 uid 参数，导致任何客户端猜到别人的行 id 就能替
// 对方 ack 掉一条通知——对方因此收不到它，要等下次 getGroupTopics() 全量
// 刷新才自愈。补法是把 uid 加进签名和 WHERE 子句（本次新增的
// grp_join_request/grp_join_reviewed 两种事件正好是新增调用点，顺手一并
// 修掉，不再制造第二个同类缺口）；hub.go 的 handleAckPendingGroupEvent 调用方
// 已同步改为传入 client 自己的 uid，不是消息体里的字段。
//
// As AckPendingClear: deleted once every device of the user has it.
func (s *Store) AckPendingGroupEvent(id int64, uid, device string) error {
	return s.ackPending(queueGroupEvent, "im_pending_group_events", id, uid, device)
}

// DropPendingGroupEvent deletes a queued event for all of the user's devices:
// one that is stale (no longer a member) or of an unknown kind.
func (s *Store) DropPendingGroupEvent(id int64, uid string) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM im_pending_group_events WHERE id = $1 AND uid = $2`, id, uid)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			_, err = tx.Exec(`DELETE FROM im_pending_acks WHERE queue = $1 AND item_id = $2`, queueGroupEvent, id)
		}
		return err
	})
}

// / ReapExpiredPendingGroupEvents drops notices nobody acked within
// pendingQueueMaxAge of now --
// the backstop for clients too old to understand ack_group_event. Safe to
// drop unconditionally: group membership itself lives in im_topic_members,
// and a client that reconnects and calls getGroupTopics() gets the
// authoritative picture regardless of this queue. The queue only shortens
// the window before that happens.
func (s *Store) ReapExpiredPendingGroupEvents(now time.Time) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM im_pending_group_events WHERE created_at < $1`,
		now.Add(-pendingQueueMaxAge),
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, s.reapOrphanAcks(queueGroupEvent, "im_pending_group_events")
}

// StartPendingGroupEventsReaper runs ReapExpiredPendingGroupEvents hourly.
// Launch with `go` once from main.go, alongside StartPendingClearsReaper.
func (s *Store) StartPendingGroupEventsReaper() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		n, err := s.ReapExpiredPendingGroupEvents(time.Now())
		if err != nil {
			logf("reapPendingGroupEvents: %v", err)
			continue
		}
		if n > 0 {
			logf("reapPendingGroupEvents: dropped %d expired group event(s)", n)
		}
	}
}
