// Store: end-to-end encryption identities, envelopes and group flag (optional module).

package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// -- E2EE --

// E2eIdentityRow is the currently-published X3DH+PQXDH bundle for one
// account, minus the one-time prekey (that's fetched separately and
// atomically by ConsumeOneE2eOneTimePreKey — see its doc comment for why
// it can't just be a column here).
type E2eIdentityRow struct {
	DeviceID              int
	RegistrationID        int
	IdentityKey           string
	SignedPreKeyID        int
	SignedPreKey          string
	SignedPreKeySignature string
	KyberPreKeyID         int
	KyberPreKey           string
	KyberPreKeySignature  string
}

// UpsertE2eIdentity replaces uid's published bundle and tops up its one-time
// prekey pool. A device republishing supersedes whatever it (or an earlier
// device on the same account — see im_e2e_identities' doc comment)
// published before; old one-time keys are left alone rather than wiped,
// since a key already handed out via ConsumeOneE2eOneTimePreKey may still be
// mid-use by a session someone is establishing right now.
func (s *Store) UpsertE2eIdentity(uid string, deviceID int, p *E2ePublishBundlePayload) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO im_e2e_identities (
			uid, device_id, registration_id, identity_key,
			signed_pre_key_id, signed_pre_key, signed_pre_key_signature,
			kyber_pre_key_id, kyber_pre_key, kyber_pre_key_signature, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW())
		ON CONFLICT (uid) DO UPDATE SET
			device_id = EXCLUDED.device_id,
			registration_id = EXCLUDED.registration_id,
			identity_key = EXCLUDED.identity_key,
			signed_pre_key_id = EXCLUDED.signed_pre_key_id,
			signed_pre_key = EXCLUDED.signed_pre_key,
			signed_pre_key_signature = EXCLUDED.signed_pre_key_signature,
			kyber_pre_key_id = EXCLUDED.kyber_pre_key_id,
			kyber_pre_key = EXCLUDED.kyber_pre_key,
			kyber_pre_key_signature = EXCLUDED.kyber_pre_key_signature,
			updated_at = NOW()
	`, uid, deviceID, p.RegistrationId, p.IdentityKey,
		p.SignedPreKeyId, p.SignedPreKey, p.SignedPreKeySignature,
		p.KyberPreKeyId, p.KyberPreKey, p.KyberPreKeySignature); err != nil {
		return fmt.Errorf("upsert identity: %w", err)
	}

	for _, otpk := range p.OneTimePreKeys {
		if _, err := tx.Exec(`
			INSERT INTO im_e2e_one_time_pre_keys (uid, key_id, pre_key)
			VALUES ($1, $2, $3)
			ON CONFLICT (uid, key_id) DO NOTHING
		`, uid, otpk.KeyId, otpk.PreKey); err != nil {
			return fmt.Errorf("insert one-time prekey %d: %w", otpk.KeyId, err)
		}
	}

	return tx.Commit()
}

// GetE2eIdentity returns (nil, nil) if uid has never published a bundle —
// that is a legitimate, common state (most accounts have never opened an
// encrypted chat) and must be distinguishable from a real DB failure so
// handleE2eFetchBundle can answer Found:false instead of surfacing an
// internal error for it.
func (s *Store) GetE2eIdentity(uid string) (*E2eIdentityRow, error) {
	var r E2eIdentityRow
	err := s.db.QueryRow(`
		SELECT device_id, registration_id, identity_key,
			signed_pre_key_id, signed_pre_key, signed_pre_key_signature,
			kyber_pre_key_id, kyber_pre_key, kyber_pre_key_signature
		FROM im_e2e_identities WHERE uid = $1
	`, uid).Scan(&r.DeviceID, &r.RegistrationID, &r.IdentityKey,
		&r.SignedPreKeyID, &r.SignedPreKey, &r.SignedPreKeySignature,
		&r.KyberPreKeyID, &r.KyberPreKey, &r.KyberPreKeySignature)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ConsumeOneE2eOneTimePreKey atomically pops (marks consumed and returns)
// one still-unused one-time prekey for uid, or found=false if the pool is
// empty. FOR UPDATE SKIP LOCKED — not a plain SELECT-then-UPDATE — is what
// makes this safe under concurrency: two people fetching this account's
// bundle at the same moment must never be handed the same one-time key, and
// SKIP LOCKED means the second concurrent caller instantly moves on to the
// next available row instead of blocking behind the first caller's
// still-open transaction.
func (s *Store) ConsumeOneE2eOneTimePreKey(uid string) (keyID int, preKey string, found bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, "", false, err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRow(`
		SELECT id, key_id, pre_key FROM im_e2e_one_time_pre_keys
		WHERE uid = $1 AND consumed = false
		ORDER BY id ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, uid).Scan(&id, &keyID, &preKey)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	if _, err := tx.Exec(`UPDATE im_e2e_one_time_pre_keys SET consumed = true WHERE id = $1`, id); err != nil {
		return 0, "", false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, "", false, err
	}
	return keyID, preKey, true, nil
}

// CountUnconsumedE2eOneTimePreKeys reports how many one-time prekeys uid
// still has sitting in the pool unconsumed — a plain read, no locking needed
// (unlike ConsumeOneE2eOneTimePreKey, nothing here is handed out or mutated).
// Used by e2e_otpk_count so the client can decide whether a fresh top-up
// batch is actually needed before publishing one.
func (s *Store) CountUnconsumedE2eOneTimePreKeys(uid string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM im_e2e_one_time_pre_keys WHERE uid = $1 AND consumed = false`, uid).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// SetGroupEncrypted flips a group topic's encrypted flag on. Deliberately
// has no "off" counterpart — see ActionE2eSetGroupEncrypted's doc comment.
func (s *Store) SetGroupEncrypted(topic string) error {
	_, err := s.db.Exec(`UPDATE im_topics SET encrypted = true, updated_at = NOW() WHERE topic = $1 AND is_group = true`, topic)
	return err
}

// IsGroupEncrypted reports a group topic's current encrypted flag.
// err == sql.ErrNoRows style "topic doesn't exist" surfaces as (false, err)
// like every other single-row lookup in this file — callers fail closed on
// error rather than assuming an unencrypted default for a topic they
// couldn't actually verify.
func (s *Store) IsGroupEncrypted(topic string) (bool, error) {
	var encrypted bool
	err := s.db.QueryRow(`SELECT encrypted FROM im_topics WHERE topic = $1`, topic).Scan(&encrypted)
	if err != nil {
		return false, err
	}
	return encrypted, nil
}

// AreCommonEncryptedGroupMembers reports whether uidA and uidB currently
// share membership in at least one ENCRYPTED group — see e2ePeerAllowed's
// doc comment for why this is a valid E2EE trust boundary on its own,
// independent of whether the two are 1:1 friends.
func (s *Store) AreCommonEncryptedGroupMembers(uidA, uidB string) (bool, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM im_topic_members m1
		JOIN im_topic_members m2 ON m1.topic = m2.topic
		JOIN im_topics t ON t.topic = m1.topic
		WHERE m1.uid = $1 AND m2.uid = $2 AND t.is_group = true AND t.encrypted = true
	`, uidA, uidB).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// E2eEnvelope is one queued-for-offline-delivery encrypted payload — see
// im_e2e_envelopes' table comment for the retention story.
type E2eEnvelope struct {
	ID      int64
	Topic   string
	FromUID string
	Kind    string
	Payload string
	MsgType int
}

// EnqueueE2eEnvelope persists one encrypted payload for a currently-offline
// recipient. handleE2eSend only ever calls this on the offline branch — an
// online recipient is handed the payload directly over their live socket and
// this table is never touched for them at all (see that handler).
func (s *Store) EnqueueE2eEnvelope(topic, toUID, fromUID, kind, payload string, msgType int, ttl time.Duration) error {
	_, err := s.db.Exec(`
		INSERT INTO im_e2e_envelopes (topic, to_uid, from_uid, kind, payload, msg_type, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW() + $7::interval)
	`, topic, toUID, fromUID, kind, payload, msgType, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	return err
}

// PopE2eEnvelopesForUID atomically returns and deletes every envelope queued
// for uid, oldest first, capped at 500 the same way GetPendingClears is —
// a login is not the place to let a pathological backlog stall the ack.
// DELETE...RETURNING makes "fetch" and "consume" a single round trip so a
// client that reconnects twice in quick succession can never be handed the
// same envelope twice.
func (s *Store) PopE2eEnvelopesForUID(uid string) ([]E2eEnvelope, error) {
	rows, err := s.db.Query(`
		DELETE FROM im_e2e_envelopes WHERE id IN (
			SELECT id FROM im_e2e_envelopes WHERE to_uid = $1 ORDER BY id ASC LIMIT 500
		)
		RETURNING id, topic, from_uid, kind, payload, msg_type
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []E2eEnvelope
	for rows.Next() {
		var e E2eEnvelope
		if err := rows.Scan(&e.ID, &e.Topic, &e.FromUID, &e.Kind, &e.Payload, &e.MsgType); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// ReapExpiredE2eEnvelopes deletes envelopes past expires_at regardless of
// delivery status — an encrypted message nobody was ever online to receive
// expires the same way an unanswered push notification would (see the
// table's doc comment), it does not accumulate as a permanent server-side
// copy.
func (s *Store) ReapExpiredE2eEnvelopes() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM im_e2e_envelopes WHERE expires_at < NOW()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// StartE2eEnvelopeReaper mirrors StartPendingClearsReaper's shape exactly —
// same hourly cadence, same "log and continue" error handling — for the
// same reason: a transient DB hiccup on one tick must not take the whole
// background sweep down.
func (s *Store) StartE2eEnvelopeReaper() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		n, err := s.ReapExpiredE2eEnvelopes()
		if err != nil {
			logf("reapE2eEnvelopes: %v", err)
			continue
		}
		if n > 0 {
			logf("reapE2eEnvelopes: dropped %d expired e2e envelope(s)", n)
		}
	}
}

// CountEncryptedGroups counts the groups marked encrypted. im_topics.encrypted
// belongs to the core schema, so this works with the e2ee module absent.
func (s *Store) CountEncryptedGroups() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM im_topics WHERE is_group AND encrypted`).Scan(&n)
	return n, err
}
