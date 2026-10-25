// Store: scan-to-join tokens, settings and requests (optional module).

package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GroupJoinSettings is the per-group scan-to-join approval configuration
// (see im_group_settings.require_join_approval). Same table as
// GroupAiSettings, different column -- kept as its own struct/functions
// because the two settings have different owner-only vs owner+admin
// authorization tiers at the WS handler layer.
type GroupJoinSettings struct {
	Topic           string
	RequireApproval bool
}

func defaultGroupJoinSettings(topic string) *GroupJoinSettings {
	return &GroupJoinSettings{Topic: topic, RequireApproval: false}
}

// GetGroupJoinSettings reads whether a group currently requires approval to
// join, or the documented default (false) if the group has never touched
// any im_group_settings row yet.
func (s *Store) GetGroupJoinSettings(topic string) (*GroupJoinSettings, error) {
	var g GroupJoinSettings
	err := s.db.QueryRow(`
		SELECT topic, require_join_approval FROM im_group_settings WHERE topic = $1
	`, topic).Scan(&g.Topic, &g.RequireApproval)
	if err == sql.ErrNoRows {
		return defaultGroupJoinSettings(topic), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get group join settings: %w", err)
	}
	return &g, nil
}

// UpsertGroupJoinSettings creates or updates require_join_approval only --
// ON CONFLICT DO UPDATE names just this column, so a group's AI-QA settings
// (or vice versa, UpsertGroupAiSettings touching this same row) are never
// clobbered by the other feature's write, same pattern as this file's
// ai_daily_quota/ai_cooldown_sec omission above. The WS handler
// (handleUpdateGroupJoinSettings) must already have verified the caller is
// the group owner before calling this -- this method does not re-check.
func (s *Store) UpsertGroupJoinSettings(topic string, requireApproval bool, updatedBy string) error {
	_, err := s.db.Exec(`
		INSERT INTO im_group_settings (topic, require_join_approval, updated_by, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (topic) DO UPDATE SET
			require_join_approval = EXCLUDED.require_join_approval,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
	`, topic, requireApproval, updatedBy)
	if err != nil {
		return fmt.Errorf("upsert group join settings: %w", err)
	}
	return nil
}

// GroupInviteToken is a group's currently active scan-to-join QR token.
type GroupInviteToken struct {
	Topic     string
	Token     string
	CreatedBy string
	ExpiresAt time.Time
}

// generateGroupQrToken returns a cryptographically random, URL-safe token.
// Unlike RandString (math/rand, seeded once at startup, used only for
// cosmetic grp_ topic-id suffixes -- see hub.go), a group QR token backs
// group_join when require_join_approval is off: it IS the entire access
// control for joining the group, so it must be genuinely unguessable. 24
// random bytes -> 32 base64url characters.
func generateGroupQrToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate group qr token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// GetActiveGroupQrToken returns the group's current token if one exists and
// has not expired, or (nil, nil) if the caller should mint a fresh one.
func (s *Store) GetActiveGroupQrToken(topic string) (*GroupInviteToken, error) {
	var t GroupInviteToken
	err := s.db.QueryRow(`
		SELECT topic, token, created_by, expires_at
		FROM im_group_invite_tokens WHERE topic = $1 AND expires_at > NOW()
	`, topic).Scan(&t.Topic, &t.Token, &t.CreatedBy, &t.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active group qr token: %w", err)
	}
	return &t, nil
}

// MintGroupQrToken generates a fresh token and replaces whatever the group
// had before (upsert on the topic PK -- a group has at most one live token
// at a time). "Refreshing" a group's QR code is exactly this: the old token
// row is overwritten and can never resolve again, no separate revocation
// step needed.
func (s *Store) MintGroupQrToken(topic, createdBy string, ttl time.Duration) (*GroupInviteToken, error) {
	token, err := generateGroupQrToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(ttl)
	_, err = s.db.Exec(`
		INSERT INTO im_group_invite_tokens (topic, token, created_by, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (topic) DO UPDATE SET
			token = EXCLUDED.token,
			created_by = EXCLUDED.created_by,
			created_at = NOW(),
			expires_at = EXCLUDED.expires_at
	`, topic, token, createdBy, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("mint group qr token: %w", err)
	}
	return &GroupInviteToken{Topic: topic, Token: token, CreatedBy: createdBy, ExpiresAt: expiresAt}, nil
}

// ResolveGroupQrToken maps a scanned token back to its group topic, or
// ("", false, nil) if the token does not exist or has expired -- an old,
// refreshed-away token failing to resolve is the intended revocation
// mechanism, not a bug.
func (s *Store) ResolveGroupQrToken(token string) (topic string, ok bool, err error) {
	err = s.db.QueryRow(`
		SELECT topic FROM im_group_invite_tokens WHERE token = $1 AND expires_at > NOW()
	`, token).Scan(&topic)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("resolve group qr token: %w", err)
	}
	return topic, true, nil
}

// Join-request lifecycle states. Terminal states (approved/rejected) are
// never deleted -- ReviewGroupJoinRequest's WHERE status='pending' clause
// depends on a prior review's row still being there to correctly reject a
// second concurrent review of the same request.
const (
	JoinRequestPending  = "pending"
	JoinRequestApproved = "approved"
	JoinRequestRejected = "rejected"
)

// ErrDuplicateJoinRequest is returned by CreateGroupJoinRequest when the
// requester already has a pending request on this topic -- the caller
// should treat this as "you already applied, please wait", not an error.
var ErrDuplicateJoinRequest = errors.New("a pending join request already exists")

// GroupJoinRequest is one scan-to-join request, pending or already reviewed.
type GroupJoinRequest struct {
	ID           int64
	Topic        string
	RequesterUID string
	Message      string
	Status       string
	ReviewedBy   string
	CreatedAt    time.Time
}

// CreateGroupJoinRequest files a new join request. lib/pq is imported only
// for its driver side effect in this file (see the GetLastSeenBatch comment
// above), so rather than type-asserting a *pq.Error this checks the unique
// index name lib/pq puts in the error text -- exactly the same
// duplicate-collapse contract Postgres already enforces via
// idx_im_group_join_requests_pending, just translated into a friendly
// sentinel error instead of a raw driver error string.
func (s *Store) CreateGroupJoinRequest(topic, requesterUID, message string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO im_group_join_requests (topic, requester_uid, message)
		VALUES ($1, $2, $3)
		RETURNING id
	`, topic, requesterUID, message).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "idx_im_group_join_requests_pending") {
			return 0, ErrDuplicateJoinRequest
		}
		return 0, fmt.Errorf("create group join request: %w", err)
	}
	return id, nil
}

// GetPendingGroupJoinRequests lists a group's requests awaiting review,
// oldest first so an admin clears a backlog in submission order.
func (s *Store) GetPendingGroupJoinRequests(topic string) ([]GroupJoinRequest, error) {
	rows, err := s.db.Query(`
		SELECT id, topic, requester_uid, message, status, reviewed_by, created_at
		FROM im_group_join_requests WHERE topic = $1 AND status = $2
		ORDER BY id ASC
	`, topic, JoinRequestPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupJoinRequest
	for rows.Next() {
		var r GroupJoinRequest
		if err := rows.Scan(&r.ID, &r.Topic, &r.RequesterUID, &r.Message, &r.Status, &r.ReviewedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReviewGroupJoinRequest transitions one pending request to approved or
// rejected. The WHERE status=$4 clause is the concurrency guard: if two
// admins review the same request at once, only the first UPDATE matches any
// rows -- the second gets (nil, nil) back and the caller must treat that as
// "someone already reviewed this", not silently re-apply their own verdict.
func (s *Store) ReviewGroupJoinRequest(id int64, approve bool, reviewedBy string) (*GroupJoinRequest, error) {
	status := JoinRequestRejected
	if approve {
		status = JoinRequestApproved
	}
	var r GroupJoinRequest
	err := s.db.QueryRow(`
		UPDATE im_group_join_requests
		SET status = $2, reviewed_by = $3, reviewed_at = NOW()
		WHERE id = $1 AND status = $4
		RETURNING id, topic, requester_uid, message, status, reviewed_by, created_at
	`, id, status, reviewedBy, JoinRequestPending).Scan(&r.ID, &r.Topic, &r.RequesterUID, &r.Message, &r.Status, &r.ReviewedBy, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("review group join request: %w", err)
	}
	return &r, nil
}
