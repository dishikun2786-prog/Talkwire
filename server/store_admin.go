// Store: read-only queries for the admin bridge.

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// GetAdminTopics returns compact summaries for conversations that contain uid.
// It is called only via the private main-API bridge after an administrator has
// selected an app user; no end-user endpoint reaches this method.
//
// Both LATERAL subqueries carry `recalled_at IS NULL` -- see GetMessages for
// why every message read needs it. They were missing it: the preview column
// could sit on a message the sender had already recalled (showing its content
// to an administrator after it had been withdrawn from every participant), and
// the message count included tombstone rows, so a conversation whose entire
// visible history was recalled still reported a non-zero total. Being an
// admin-only path does not change the answer -- recall means the content is
// gone, not merely hidden from peers.
func (s *Store) GetAdminTopics(uid string, limit int) ([]AdminTopicInfo, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT t.topic, t.is_group, COALESCE(t.name, ''), t.last_seq,
		       t.clear_version, t.clear_before_seq,
		       COALESCE(last_msg.content, ''), COALESCE(last_msg.content_type, ''),
		       COALESCE(EXTRACT(EPOCH FROM last_msg.created_at)::bigint * 1000, 0),
		       COALESCE(msg_count.count, 0)
		FROM im_topics t
		JOIN im_topic_members own ON own.topic = t.topic AND own.uid = $1
		LEFT JOIN LATERAL (
			SELECT content, content_type, created_at
			FROM im_messages WHERE topic = t.topic AND recalled_at IS NULL
			ORDER BY seq DESC LIMIT 1
		) last_msg ON TRUE
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS count FROM im_messages
			WHERE topic = t.topic AND recalled_at IS NULL
		) msg_count ON TRUE
		ORDER BY COALESCE(last_msg.created_at, t.updated_at) DESC
		LIMIT $2
	`, uid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []AdminTopicInfo
	for rows.Next() {
		var t AdminTopicInfo
		if err := rows.Scan(&t.Topic, &t.IsGroup, &t.Name, &t.LastSeq,
			&t.ClearVersion, &t.ClearBeforeSeq, &t.LastContent, &t.LastContentType,
			&t.LastMessageAt, &t.MessageCount); err != nil {
			return nil, err
		}
		members, err := s.GetTopicMemberUIDs(t.Topic)
		if err != nil {
			return nil, err
		}
		t.Members = members
		topics = append(topics, t)
	}
	return topics, rows.Err()
}

// GetAdminTopic reads membership and clear state for authorization checks in
// the main API before it renders or clears a conversation.
func (s *Store) GetAdminTopic(topic string) (*AdminTopicInfo, error) {
	var t AdminTopicInfo
	err := s.db.QueryRow(`
		SELECT topic, is_group, COALESCE(name, ''), last_seq, clear_version, clear_before_seq
		FROM im_topics WHERE topic = $1
	`, topic).Scan(&t.Topic, &t.IsGroup, &t.Name, &t.LastSeq, &t.ClearVersion, &t.ClearBeforeSeq)
	if err != nil {
		return nil, err
	}
	members, err := s.GetTopicMemberUIDs(topic)
	if err != nil {
		return nil, err
	}
	t.Members = members
	return &t, nil
}

// GetAdminMessages pages history newest-first under optional server-side
// filters. Returning one extra row makes hasMore accurate without an expensive
// COUNT(*) over every message in a large conversation.
func (s *Store) GetAdminMessages(topic string, beforeSeq, limit int, kind, keyword string, fromMs, toMs int64) ([]StoredMessage, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	// recalled_at IS NULL seeds the filter list rather than being appended
	// later, so every optional filter below narrows an already-recall-free set.
	// It was absent entirely, which put recalled message bodies back on the
	// admin IM-records page -- including via the keyword filter, which would
	// happily ILIKE against content the sender had withdrawn.
	where := []string{"topic = $1", "recalled_at IS NULL"}
	args := []interface{}{topic}
	next := 2
	add := func(condition string, value interface{}) {
		where = append(where, fmt.Sprintf(condition, next))
		args = append(args, value)
		next++
	}
	if beforeSeq > 0 {
		add("seq < $%d", beforeSeq)
	}
	switch kind {
	case "text":
		where = append(where, "content_type = 'text'")
	case "image", "video", "audio":
		where = append(where, "content_type LIKE '"+kind+"/%'")
	case "file":
		where = append(where, "content_type NOT IN ('text', 'system') AND content_type NOT LIKE 'image/%' AND content_type NOT LIKE 'video/%' AND content_type NOT LIKE 'audio/%'")
	}
	if keyword != "" {
		add("content ILIKE '%%' || $%d || '%%'", keyword)
	}
	if fromMs > 0 {
		add("created_at >= to_timestamp($%d / 1000.0)", fromMs)
	}
	if toMs > 0 {
		add("created_at <= to_timestamp($%d / 1000.0)", toMs)
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(`
		SELECT seq, from_uid, content, content_type,
		       COALESCE(file_url, ''), COALESCE(file_name, ''),
		       (EXTRACT(EPOCH FROM created_at) * 1000)::bigint,
		       extra, duration, sender_kind, forward_from_msg_id, forward_from_topic
		FROM im_messages
		WHERE %s
		ORDER BY seq DESC LIMIT $%d
	`, strings.Join(where, " AND "), next)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var messages []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var extra sql.NullString
		var forwardFromMsgID sql.NullInt64
		var forwardFromTopic sql.NullString
		if err := rows.Scan(&m.Seq, &m.From, &m.Content, &m.ContentType,
			&m.FileURL, &m.FileName, &m.Timestamp, &extra, &m.Duration,
			&m.SenderKind, &forwardFromMsgID, &forwardFromTopic); err != nil {
			return nil, false, err
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
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	// The timeline is rendered chronologically even though this query pages
	// backwards from the most recent sequence.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, hasMore, nil
}
