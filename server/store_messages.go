// Store: messages, sequence numbers, history, recall, clear and read cursors.

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// GetNextSeq returns the next message sequence number for a topic
func (s *Store) GetNextSeq(topic string) (int, error) {
	var seq int
	err := s.db.QueryRow(`
		INSERT INTO im_topics (topic, is_group, last_seq)
		VALUES ($1, false, 1)
		ON CONFLICT (topic) DO UPDATE
			SET last_seq = im_topics.last_seq + 1
		RETURNING im_topics.last_seq
	`, topic).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("get next seq: %w", err)
	}
	return seq, nil
}

// SaveMessage persists a message to the database
func (s *Store) SaveMessage(topic, fromUID, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int) (int, error) {
	seq, err := s.GetNextSeq(topic)
	if err != nil {
		return 0, err
	}
	// A nil json.RawMessage (the common case: no "extra" field on plain
	// text/image/voice-without-duration messages) does not reliably convert
	// to SQL NULL through database/sql's reflection-based parameter binding
	// for named []byte types — lib/pq was sending it as an empty, non-JSON
	// string and Postgres rejected it with "invalid input syntax for type
	// json", failing every send that omitted extra. Pass an explicit
	// interface{}(nil) instead, which is unambiguous.
	var extraParam interface{}
	if len(extra) > 0 {
		extraParam = []byte(extra)
	}
	_, err = s.db.Exec(`
		INSERT INTO im_messages (topic, seq, from_uid, content, content_type, file_url, file_name, extra, duration)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, topic, seq, fromUID, content, contentType, nullIfEmpty(fileURL), nullIfEmpty(fileName), extraParam, duration)
	if err != nil {
		return 0, fmt.Errorf("save message: %w", err)
	}
	return seq, nil
}

// GetNextSeqTx is the transaction-scoped twin of GetNextSeq, used by
// handleForwardBundle so every seq assignment in a bundle forward is part of
// the same all-or-nothing transaction as the inserts that consume it.
func (s *Store) GetNextSeqTx(tx *sql.Tx, topic string) (int, error) {
	var seq int
	err := tx.QueryRow(`
		INSERT INTO im_topics (topic, is_group, last_seq)
		VALUES ($1, false, 1)
		ON CONFLICT (topic) DO UPDATE
			SET last_seq = im_topics.last_seq + 1
		RETURNING im_topics.last_seq
	`, topic).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("get next seq (tx): %w", err)
	}
	return seq, nil
}

// SaveMessageFull persists a message together with its sender-kind tag and
// optional forward provenance. It supersedes SaveMessage as the primitive
// behind Hub.deliverMessage (see hub.go); SaveMessage itself is left as-is
// since it's a lower-level building block with no reason to grow five more
// parameters for a single remaining caller path.
func (s *Store) SaveMessageFull(topic, fromUID, senderKind, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int, forwardFromMsgID *int, forwardFromTopic *string, forwardChainDepth int) (int, error) {
	// M6: seq allocation and the row insert now share one transaction. They
	// used to be two independent statements (GetNextSeq against the pool,
	// then SaveMessageFullTx(nil, ...) also directly against the pool) --
	// any failure in between (a save error, a crash, a dropped DB
	// connection) advanced im_topics.last_seq without ever inserting the row
	// it was allocated for, leaving a permanent seq gap on every client that
	// syncs this topic. Wrapping both in a transaction and rolling back on
	// any error means a failed save genuinely never consumed a seq at all --
	// GetNextSeqTx's UPDATE to last_seq is undone along with everything else.
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	seq, err := s.GetNextSeqTx(tx, topic)
	if err != nil {
		return 0, err
	}
	if err := s.SaveMessageFullTx(tx, topic, seq, fromUID, senderKind, content, contentType, fileURL, fileName, extra, duration, forwardFromMsgID, forwardFromTopic, forwardChainDepth); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return seq, nil
}

// SaveMessageFullTx inserts an already-seq-assigned message row, either
// inside a caller-provided transaction (tx != nil, used by
// handleForwardBundle) or directly against the pool (tx == nil, used by
// SaveMessageFull).
func (s *Store) SaveMessageFullTx(tx *sql.Tx, topic string, seq int, fromUID, senderKind, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int, forwardFromMsgID *int, forwardFromTopic *string, forwardChainDepth int) error {
	// See SaveMessage's comment: a nil json.RawMessage must become an
	// explicit interface{}(nil), not a zero-length []byte, or lib/pq sends an
	// empty non-JSON string and Postgres rejects it.
	var extraParam interface{}
	if len(extra) > 0 {
		extraParam = []byte(extra)
	}
	const q = `
		INSERT INTO im_messages (
			topic, seq, from_uid, sender_kind, content, content_type,
			file_url, file_name, extra, duration,
			forward_from_msg_id, forward_from_topic, forward_chain_depth
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`
	args := []interface{}{
		topic, seq, fromUID, senderKind, content, contentType,
		nullIfEmpty(fileURL), nullIfEmpty(fileName), extraParam, duration,
		forwardFromMsgID, forwardFromTopic, forwardChainDepth,
	}
	var err error
	if tx != nil {
		_, err = tx.Exec(q, args...)
	} else {
		_, err = s.db.Exec(q, args...)
	}
	if err != nil {
		return fmt.Errorf("save message (full): %w", err)
	}
	return nil
}

// SaveMessageFullIdempotent is SaveMessageFull's dedup-aware sibling, used by
// deliverMessage whenever the caller supplies a non-empty clientMsgID (in
// practice: only handlePub, since that's the only write path that can be
// resent by a client that never saw its own ack -- see H3 in the IM audit).
//
// The problem this closes: a pub's ack can be lost after the server already
// persisted and broadcast the message (connection drops mid-round-trip is
// the common case -- see ImWebSocketClient.kt's PUBLISH_PENDING/onClosed
// handling). The client, having no ack, cannot tell "never arrived" apart
// from "arrived, ack lost" and must be able to safely resend either way.
// Without a stable identity for "this is logically the same send", a resend
// becomes a second row with a new seq -- a duplicate message every
// recipient (and the sender, on their next history pull) sees twice.
//
// clientMsgID is that stable identity: the client's own localId, unique per
// logical send and unchanged across retries of the SAME send (see
// ChatRepository.kt's retryLocalId). Given that, "have I already saved a
// row for (topic, clientMsgID)" is exactly the question that answers "is
// this a genuine new message or a resend of one I already have".
//
// Race safety: the fast pre-check below is not itself the enforcement --
// two goroutines racing the exact same (topic, clientMsgID) is only
// possible if a client somehow fires the identical pub twice concurrently,
// which sendJsonSync's single-in-flight-per-key design shouldn't produce,
// but the DB is the actual backstop regardless: idx_im_messages_topic_
// client_msg_id (unique, partial) means the losing INSERT ... ON CONFLICT
// DO NOTHING affects zero rows, and the code below re-selects the winner's
// row inside the same transaction rather than assuming its own seq stuck.
func (s *Store) SaveMessageFullIdempotent(topic, clientMsgID, fromUID, senderKind, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int, forwardFromMsgID *int, forwardFromTopic *string, forwardChainDepth int) (seq int, ts time.Time, duplicate bool, err error) {
	// Fast path: this exact (topic, clientMsgID) has already been saved.
	// Skips burning a seq number on the overwhelmingly common case (a
	// resend of a pub whose ack was lost, not a genuine race).
	var existingSeq int
	var existingTs time.Time
	preErr := s.db.QueryRow(
		`SELECT seq, created_at FROM im_messages WHERE topic = $1 AND client_msg_id = $2`,
		topic, clientMsgID,
	).Scan(&existingSeq, &existingTs)
	if preErr == nil {
		return existingSeq, existingTs, true, nil
	}
	if preErr != sql.ErrNoRows {
		return 0, time.Time{}, false, fmt.Errorf("check existing client_msg_id: %w", preErr)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, time.Time{}, false, err
	}
	defer tx.Rollback()

	seq, err = s.GetNextSeqTx(tx, topic)
	if err != nil {
		return 0, time.Time{}, false, err
	}

	var extraParam interface{}
	if len(extra) > 0 {
		extraParam = []byte(extra)
	}
	const insertQ = `
		INSERT INTO im_messages (
			topic, seq, from_uid, sender_kind, content, content_type,
			file_url, file_name, extra, duration,
			forward_from_msg_id, forward_from_topic, forward_chain_depth,
			client_msg_id
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (topic, client_msg_id) WHERE client_msg_id IS NOT NULL DO NOTHING
		RETURNING created_at
	`
	var insertedTs time.Time
	insErr := tx.QueryRow(insertQ,
		topic, seq, fromUID, senderKind, content, contentType,
		nullIfEmpty(fileURL), nullIfEmpty(fileName), extraParam, duration,
		forwardFromMsgID, forwardFromTopic, forwardChainDepth, clientMsgID,
	).Scan(&insertedTs)

	if insErr == sql.ErrNoRows {
		// Lost the race: another connection's insert for this exact
		// clientMsgID committed between our pre-check and here. This seq
		// number is now orphaned (im_topics.last_seq was already advanced by
		// GetNextSeqTx and the transaction hasn't committed it to a row) --
		// an accepted, narrow-window cost of keeping the fast path cheap,
		// consistent with M6's documented seq-gap tolerance elsewhere in
		// this file. Re-select the winner's row so the caller still gets a
		// correct, existing seq/ts to hand back to its client rather than
		// erroring out a pub that in fact already succeeded once.
		if selErr := tx.QueryRow(
			`SELECT seq, created_at FROM im_messages WHERE topic = $1 AND client_msg_id = $2`,
			topic, clientMsgID,
		).Scan(&existingSeq, &existingTs); selErr != nil {
			return 0, time.Time{}, false, fmt.Errorf("re-select after conflict: %w", selErr)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return 0, time.Time{}, false, commitErr
		}
		return existingSeq, existingTs, true, nil
	}
	if insErr != nil {
		return 0, time.Time{}, false, fmt.Errorf("save message (idempotent): %w", insErr)
	}
	if err := tx.Commit(); err != nil {
		return 0, time.Time{}, false, err
	}
	return seq, insertedTs, false, nil
}

// ForwardableMessage carries the fields needed to duplicate a message into
// another topic as a forward, plus its own forward_chain_depth so the caller
// can enforce the depth limit before re-forwarding it further.
type ForwardableMessage struct {
	Seq               int
	FromUID           string
	SenderKind        string
	Content           string
	ContentType       string
	FileURL           string
	FileName          string
	Extra             json.RawMessage
	Duration          int
	ForwardChainDepth int
}

// GetMessageForForward reads a single message's forward-relevant fields by
// (topic, seq) — the unit both forward_bundle (sourceMsgIds) and
// forward_broadcast (sourceMsgId) address messages by, since that's the only
// identifier the client ever sees for a message. Returns sql.ErrNoRows
// (wrapped) when not found; callers translate that into "source_not_found".
//
// `recalled_at IS NULL` for the same reason the read paths carry it, but
// with a sharper consequence: a recalled row is stripped to an empty
// content and a NULL file_url, so without this predicate forwarding a
// message the sender just recalled would silently succeed and insert a
// blank message into the target topic instead of reporting
// "source_not_found".
func (s *Store) GetMessageForForward(topic string, seq int) (*ForwardableMessage, error) {
	var m ForwardableMessage
	var extra sql.NullString
	err := s.db.QueryRow(`
		SELECT seq, from_uid, sender_kind, content, content_type,
		       COALESCE(file_url, ''), COALESCE(file_name, ''),
		       extra, duration, forward_chain_depth
		FROM im_messages
		WHERE topic = $1 AND seq = $2 AND recalled_at IS NULL
	`, topic, seq).Scan(&m.Seq, &m.FromUID, &m.SenderKind, &m.Content, &m.ContentType,
		&m.FileURL, &m.FileName, &extra, &m.Duration, &m.ForwardChainDepth)
	if err != nil {
		return nil, err
	}
	if extra.Valid && extra.String != "" && extra.String != "null" {
		m.Extra = json.RawMessage(extra.String)
	}
	return &m, nil
}

// GetMessages retrieves messages for a topic with pagination.
//
// `recalled_at IS NULL` is what makes SoftDeleteMessage's tombstone rows
// invisible to every reader: the row survives only to keep the (topic, seq)
// series gap-free, its content must never come back out of here. Any new
// message-reading query added to this file needs the same predicate — a
// missing one is not a compile error, it silently un-recalls messages.
func (s *Store) GetMessages(topic string, beforeSeq, limit int) ([]StoredMessage, error) {
	// 归一化只有一处实现（config.go 的 normalizeHistoryLimit），hub.handleGet
	// 也调同一个。此前这里是独立硬编码的 `>100 就压成 50`，跑在 hub 的判据之
	// 后，于是 101..200 被 hub 放行、到这里静默变成 50，且把 group_chat
	// .history_page_size 调过 100 完全不生效。这里保留一次调用是防御性的——
	// 本方法也可能被 hub 以外的调用方用到——但不要在这里另写上限。
	limit = normalizeHistoryLimit(limit)
	var rows *sql.Rows
	var err error
	if beforeSeq > 0 {
		rows, err = s.db.Query(`
			SELECT seq, from_uid, content, content_type,
			       COALESCE(file_url, ''), COALESCE(file_name, ''),
			       (EXTRACT(EPOCH FROM created_at) * 1000)::bigint,
			       extra, duration, sender_kind, forward_from_msg_id, forward_from_topic
			FROM im_messages
			WHERE topic = $1 AND seq < $2 AND recalled_at IS NULL
			ORDER BY seq DESC LIMIT $3
		`, topic, beforeSeq, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT seq, from_uid, content, content_type,
			       COALESCE(file_url, ''), COALESCE(file_name, ''),
			       (EXTRACT(EPOCH FROM created_at) * 1000)::bigint,
			       extra, duration, sender_kind, forward_from_msg_id, forward_from_topic
			FROM im_messages
			WHERE topic = $1 AND recalled_at IS NULL
			ORDER BY seq DESC LIMIT $2
		`, topic, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var msgs []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var extra sql.NullString
		var forwardFromMsgID sql.NullInt64
		var forwardFromTopic sql.NullString
		err := rows.Scan(&m.Seq, &m.From, &m.Content, &m.ContentType,
			&m.FileURL, &m.FileName, &m.Timestamp, &extra, &m.Duration,
			&m.SenderKind, &forwardFromMsgID, &forwardFromTopic)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.Topic = topic
		if extra.Valid && extra.String != "" && extra.String != "null" {
			m.Extra = json.RawMessage(extra.String)
		}
		if forwardFromMsgID.Valid {
			m.ForwardFromMsgID = int(forwardFromMsgID.Int64)
		}
		if forwardFromTopic.Valid {
			m.ForwardFromTopic = forwardFromTopic.String
		}
		msgs = append(msgs, m)
	}
	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// GetMessagesAfter retrieves messages newer than afterSeq in server sequence
// order. This is the reconnect/incremental-sync counterpart to GetMessages;
// using beforeSeq with (latest local seq + 1) only returned already cached rows
// and made offline messages silently disappear.
//
// Carries the same `recalled_at IS NULL` predicate as GetMessages — see there.
// A client that was offline when a message was both sent and recalled must
// come back to find nothing, not to find the recalled message.
func (s *Store) GetMessagesAfter(topic string, afterSeq, limit int) ([]StoredMessage, error) {
	// 同 GetMessages：唯一的归一化实现在 config.go 的 normalizeHistoryLimit。
	limit = normalizeHistoryLimit(limit)
	rows, err := s.db.Query(`
		SELECT seq, from_uid, content, content_type,
		       COALESCE(file_url, ''), COALESCE(file_name, ''),
		       (EXTRACT(EPOCH FROM created_at) * 1000)::bigint,
		       extra, duration, sender_kind, forward_from_msg_id, forward_from_topic
		FROM im_messages
		WHERE topic = $1 AND seq > $2 AND recalled_at IS NULL
		ORDER BY seq ASC LIMIT $3
	`, topic, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("get messages after: %w", err)
	}
	defer rows.Close()

	var msgs []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var extra sql.NullString
		var forwardFromMsgID sql.NullInt64
		var forwardFromTopic sql.NullString
		if err := rows.Scan(&m.Seq, &m.From, &m.Content, &m.ContentType,
			&m.FileURL, &m.FileName, &m.Timestamp, &extra, &m.Duration,
			&m.SenderKind, &forwardFromMsgID, &forwardFromTopic); err != nil {
			return nil, fmt.Errorf("scan message after: %w", err)
		}
		m.Topic = topic
		if extra.Valid && extra.String != "" && extra.String != "null" {
			m.Extra = json.RawMessage(extra.String)
		}
		if forwardFromMsgID.Valid {
			m.ForwardFromMsgID = int(forwardFromMsgID.Int64)
		}
		if forwardFromTopic.Valid {
			m.ForwardFromTopic = forwardFromTopic.String
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages after: %w", err)
	}
	return msgs, nil
}

// ClearTopicMessages hard-deletes every current message in a topic while
// recording a durable clear version/cutoff for offline-client reconciliation.
// It also returns distinct media URLs so the caller can garbage-collect blobs
// once no remaining message references them.
func (s *Store) ClearTopicMessages(topic string) (ClearTopicResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return ClearTopicResult{}, err
	}
	defer tx.Rollback()

	// Lock the topic before reading last_seq. SaveMessage increments last_seq in
	// the same row, so a concurrent publication will happen wholly before or
	// after the clear and never be accidentally deleted midway through it.
	var lastSeq, oldClearVersion int
	err = tx.QueryRow(`
		SELECT last_seq, clear_version FROM im_topics WHERE topic = $1 FOR UPDATE
	`, topic).Scan(&lastSeq, &oldClearVersion)
	if err == sql.ErrNoRows {
		return ClearTopicResult{}, fmt.Errorf("topic not found")
	}
	if err != nil {
		return ClearTopicResult{}, err
	}

	// H10: UNION (not UNION ALL) both dedupes within each SELECT and across
	// the two — a video's own file_url and its thumbUrl end up in one flat,
	// deduplicated list. $1 is bound once; Postgres lets a numbered
	// placeholder repeat within a query without a second positional arg.
	rows, err := tx.Query(`
		SELECT DISTINCT file_url FROM im_messages
		WHERE topic = $1 AND file_url IS NOT NULL AND file_url <> ''
		UNION
		SELECT DISTINCT extra->>'thumbUrl' FROM im_messages
		WHERE topic = $1 AND extra->>'thumbUrl' IS NOT NULL AND extra->>'thumbUrl' <> ''
	`, topic)
	if err != nil {
		return ClearTopicResult{}, err
	}
	var urls []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return ClearTopicResult{}, err
		}
		urls = append(urls, u)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ClearTopicResult{}, err
	}
	if err := rows.Close(); err != nil {
		return ClearTopicResult{}, err
	}

	var deleted int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM im_messages WHERE topic = $1`, topic).Scan(&deleted); err != nil {
		return ClearTopicResult{}, err
	}
	if _, err := tx.Exec(`DELETE FROM im_messages WHERE topic = $1`, topic); err != nil {
		return ClearTopicResult{}, err
	}

	clearVersion := oldClearVersion + 1
	if _, err := tx.Exec(`
		UPDATE im_topics
		SET clear_version = $2, clear_before_seq = $3, cleared_at = NOW(), updated_at = NOW()
		WHERE topic = $1
	`, topic, clearVersion, lastSeq); err != nil {
		return ClearTopicResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ClearTopicResult{}, err
	}
	return ClearTopicResult{
		DeletedMessages: deleted,
		DeletedMedia:    len(urls),
		ClearVersion:    clearVersion,
		ClearBeforeSeq:  lastSeq,
		FileURLs:        urls,
	}, nil
}

// CountFileRefs reports how many messages still reference a media blob URL, so
// a recall/clear only asks the main server to delete the underlying file once
// nothing points at it anymore (media can be shared via forwarding).
//
// H10: also matches extra->>'thumbUrl' — gcMediaBlob now gets called with
// thumbnail URLs too (see ClearTopicMessages), and forwarding a
// video reuses the same uploaded thumbnail blob across messages exactly like
// it reuses file_url. Checking file_url alone would undercount a thumbnail
// that's still alive only because ANOTHER message references it as its
// thumbUrl, and delete it out from under that still-live message.
func (s *Store) CountFileRefs(fileURL string) (int, error) {
	var n int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM im_messages WHERE file_url = $1 OR extra->>'thumbUrl' = $1
	`, fileURL).Scan(&n)
	return n, err
}

// SoftDeleteMessage is the recall primitive. It replaced a hard-DELETE
// predecessor (DeleteMessage), which has since been removed outright rather
// than left sitting here unused: a dead function whose whole job is to punch a
// permanent hole in a topic's seq series is one grep-by-name away from being
// picked up again, and a deprecation comment does not stop that.
//
// It keeps the row -- and therefore the (topic, seq) slot --
// while wiping everything the row carried, and returns the media URLs the
// message referenced (its file and, for a video, the thumbnail in
// extra->>'thumbUrl') so the caller can garbage-collect the blobs.
//
// Why a tombstone instead of a DELETE. Seq numbers come from
// im_topics.last_seq and are never reissued, so a hard delete punches a
// permanent hole in a topic's seq series. A client doing gap detection over
// its local messages cannot tell that hole apart from a message it never
// received: it asks for the missing range on every reconnect, forever, and
// is answered with nothing forever. Keeping the row makes the series
// contiguous again; GetMessages/GetMessagesAfter filter `recalled_at IS
// NULL` so no reader ever sees it.
//
// The row is stripped (content, file_url, file_name, extra) rather than just
// flagged, so "recall leaves nothing on the server" still holds literally,
// and so CountFileRefs stops counting this row as a reference to the blob --
// otherwise the tombstone would pin every recalled message's media alive.
//
// Old values are read through a CTE because a plain UPDATE ... RETURNING
// hands back the NEW (already-cleared) values. Data-modifying CTEs always
// run to completion regardless of whether the outer query reads their
// output, so the update is not conditional on the SELECT.
//
// The thumbnail used to be left behind (plan 1.9): the hard-delete
// predecessor reclaimed it (H10), this function returned only file_url, and
// the thumbnail blob stayed in storage, downloadable without authentication
// through /images/:filename.
func (s *Store) SoftDeleteMessage(topic string, seq int, fromUID string) ([]string, bool, error) {
	var fileURL, thumbURL string
	err := s.db.QueryRow(`
		WITH victim AS (
			SELECT topic, seq, COALESCE(file_url, '') AS old_file_url,
			       COALESCE(extra->>'thumbUrl', '') AS old_thumb_url
			FROM im_messages
			WHERE topic = $1 AND seq = $2 AND from_uid = $3 AND recalled_at IS NULL
		), wiped AS (
			UPDATE im_messages m
			SET recalled_at = NOW(),
			    content = '',
			    file_url = NULL,
			    file_name = NULL,
			    extra = NULL
			FROM victim v
			WHERE m.topic = v.topic AND m.seq = v.seq
			RETURNING 1
		)
		SELECT old_file_url, old_thumb_url FROM victim
	`, topic, seq, fromUID).Scan(&fileURL, &thumbURL)
	if err == sql.ErrNoRows {
		// Not found, not this caller's message, or already recalled. All
		// three are "nothing to do", not failures -- a duplicate recall from
		// a retrying client must stay idempotent.
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var urls []string
	for _, u := range []string{fileURL, thumbURL} {
		if u != "" && (len(urls) == 0 || urls[0] != u) {
			urls = append(urls, u)
		}
	}
	return urls, true, nil
}

// ReadCursor is one member's read position in a group topic: everything up
// to and including Seq is considered read by UID.
type ReadCursor struct {
	UID string
	Seq int
}

// UpsertReadCursor advances uid's read cursor in topic to seq, monotonically.
// The WHERE clause makes this safe against out-of-order delivery: a "note
// read" that arrives late for an already-superseded (lower) seq is a no-op,
// never a regression. Scoped to the caller (group_chat.read_receipt_enabled
// gate lives in the handler, not here) so this function itself has no
// opinion on whether the feature is on.
func (s *Store) UpsertReadCursor(topic, uid string, seq int) error {
	_, err := s.db.Exec(`
		INSERT INTO im_read_cursors (topic, uid, last_read_seq, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (topic, uid) DO UPDATE SET
			last_read_seq = GREATEST(im_read_cursors.last_read_seq, EXCLUDED.last_read_seq),
			updated_at = NOW()
		WHERE EXCLUDED.last_read_seq > im_read_cursors.last_read_seq
	`, topic, uid, seq)
	return err
}

// GetReadCursors returns every member's read cursor for topic, for a client
// that just opened the conversation (or reconnected) to build a baseline
// without waiting for live "note read" broadcasts it may have missed while
// offline. Bounded by group_chat.max_members (500 today), so this is always
// a small, PK-indexed scan.
func (s *Store) GetReadCursors(topic string) ([]ReadCursor, error) {
	rows, err := s.db.Query(`
		SELECT uid, last_read_seq FROM im_read_cursors WHERE topic = $1
	`, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadCursor
	for rows.Next() {
		var c ReadCursor
		if err := rows.Scan(&c.UID, &c.Seq); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TopicExists checks whether a topic (P2P or group) has already been
// created. No longer used to gate resolveP2PTopic's friendship check --
// that now checks the caller's own im_topic_members row via IsGroupMember,
// since gating on topic existence let a revoked P2P friendship's membership
// rows get silently re-inserted by the next EnsureP2PTopic call (see
// resolveP2PTopic's doc comment). Still used elsewhere for lighter existence
// checks that don't carry that authorization weight.
func (s *Store) TopicExists(topic string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM im_topics WHERE topic = $1)`, topic).Scan(&exists)
	return exists, err
}

// GetLastSeq returns the last sequence number for a topic (from cache)
func (s *Store) GetLastSeq(topic string) (int, error) {
	var seq int
	err := s.db.QueryRow(`SELECT COALESCE(last_seq, 0) FROM im_topics WHERE topic = $1`, topic).Scan(&seq)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return seq, err
}
