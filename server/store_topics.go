// Store: groups, direct-chat topics and membership.

package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sort"
)

// GetPeerUIDs returns every uid that shares at least one topic with uid —
// covering both 1:1 peers and groupmates. This is what backs a `pres` query
// for topic:"*", so a client can reconcile its whole presence picture in one
// round trip after a reconnect instead of relying on having caught every push.
func (s *Store) GetPeerUIDs(uid string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT m2.uid
		FROM im_topic_members m1
		JOIN im_topic_members m2 ON m2.topic = m1.topic AND m2.uid <> m1.uid
		WHERE m1.uid = $1
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var peers []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	return peers, rows.Err()
}

func (s *Store) GetUserTopics(uid string) ([]TopicInfo, error) {
	rows, err := s.db.Query(`
		SELECT t.topic, t.last_seq, t.is_group, t.clear_version, t.clear_before_seq
		FROM im_topics t
		JOIN im_topic_members m ON t.topic = m.topic
		WHERE m.uid = $1
		ORDER BY t.updated_at DESC
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []TopicInfo
	for rows.Next() {
		var t TopicInfo
		if err := rows.Scan(&t.Topic, &t.LastSeq, &t.IsGroup, &t.ClearVersion, &t.ClearBeforeSeq); err != nil {
			return nil, err
		}
		topics = append(topics, t)
	}
	return topics, nil
}

// CreateGroup creates a new group topic
func (s *Store) CreateGroup(ownerUID, name string, members []string) (string, error) {
	topic := "grp_" + RandString(12)
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// Create topic
	_, err = tx.Exec(`
		INSERT INTO im_topics (topic, is_group, name, owner_uid, last_seq)
		VALUES ($1, true, $2, $3, 0)
	`, topic, name, ownerUID)
	if err != nil {
		return "", fmt.Errorf("create group topic: %w", err)
	}

	// Add the creator as the group's owner row. Role passed as a bind
	// parameter rather than inlined so RoleOwner stays the single source of
	// truth for the literal (see its doc comment in protocol.go).
	_, err = tx.Exec(`
		INSERT INTO im_topic_members (topic, uid, role)
		VALUES ($1, $2, $3)
	`, topic, ownerUID, RoleOwner)
	if err != nil {
		return "", fmt.Errorf("add owner to group: %w", err)
	}

	// Add other members
	for _, m := range members {
		if m == ownerUID {
			continue
		}
		_, err = tx.Exec(`
			INSERT INTO im_topic_members (topic, uid, role)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING
		`, topic, m, RoleMember)
		if err != nil {
			return "", fmt.Errorf("add member %s: %w", m, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return topic, nil
}

// GetGroupInfo returns group details
func (s *Store) GetGroupInfo(topic string) (*GroupInfo, error) {
	var info GroupInfo
	err := s.db.QueryRow(`
		SELECT topic, name, owner_uid FROM im_topics WHERE topic = $1 AND is_group = true
	`, topic).Scan(&info.Topic, &info.Name, &info.Owner)
	if err != nil {
		return nil, err
	}

	// H8: this used to be treated as "non-fatal" and return &info with
	// Members left at its zero value (0). handleLeaveGrp's owner-leaving
	// branch reads exactly that field to decide "is this genuinely the last
	// member, safe to auto-disband" vs "must transfer ownership first" — a
	// transient COUNT failure was therefore indistinguishable from a real
	// empty group, and silently disbanded groups that still had members.
	// Every call site here already branches on err != nil and degrades
	// gracefully (skip using the info / fall back), so propagating the error
	// is strictly safer than fabricating a misleading Members=0.
	err = s.db.QueryRow(`SELECT COUNT(*) FROM im_topic_members WHERE topic = $1`, topic).Scan(&info.Members)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// GetGroupMembers returns all members of a group
func (s *Store) GetGroupMembers(topic string) ([]GroupMemberInfo, error) {
	rows, err := s.db.Query(`
		SELECT uid, role, EXTRACT(EPOCH FROM joined_at)::bigint * 1000
		FROM im_topic_members WHERE topic = $1 ORDER BY joined_at ASC
	`, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []GroupMemberInfo
	for rows.Next() {
		var m GroupMemberInfo
		if err := rows.Scan(&m.UID, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

// AddGroupMember adds a user to a group as a plain member, and is a no-op
// for anyone already in it.
//
// F-22: the ON CONFLICT clause used to be `DO UPDATE SET role = 'member'`,
// which silently demoted whoever it touched. Every "put this uid in this
// group" path runs through here -- invite, and any future ensure-member
// helper -- so re-inviting someone who was already a member (a completely
// ordinary double-tap or stale-roster action) stripped the owner of
// ownership, or an admin of their admin rights, with no error and no trace.
// Worse, im_topics.owner_uid was left untouched, so the group ended up with
// an owner_uid pointing at a row whose role said 'member': CanManageGroup's
// role check and IsGroupOwner's owner_uid check then disagreed about who
// runs the group.
//
// DO NOTHING is the correct semantic here -- "add if absent". Promotion and
// demotion have their own explicit, owner-gated entry point
// (SetGroupMemberRole) and must never be a side effect of an add.
func (s *Store) AddGroupMember(topic, uid string) error {
	_, err := s.db.Exec(`
		INSERT INTO im_topic_members (topic, uid, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (topic, uid) DO NOTHING
	`, topic, uid, RoleMember)
	return err
}

// RemoveGroupMember removes a user from a group, along with any pending
// per-member queue rows for them on that topic -- once they're no longer a
// member, GetTopicMemberUIDs will never enumerate them again, so leftover
// rows would just sit there forever.
//
// The im_pending_group_events cleanup matters for more than tidiness: a
// queued grp_sync for a topic this uid has just been kicked out of would,
// on their next login, tell their client to go refresh a group it is no
// longer allowed to read — a guaranteed-to-fail fetch presented to the user
// as a phantom group reappearing in their list.
func (s *Store) RemoveGroupMember(topic, uid string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM im_topic_members WHERE topic = $1 AND uid = $2`, topic, uid); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM im_pending_clears WHERE topic = $1 AND uid = $2`, topic, uid); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM im_pending_group_events WHERE topic = $1 AND uid = $2 AND kind = $3`, topic, uid, PendingGroupEventSync); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveP2PMembers revokes both sides' membership on a P2P topic without
// touching im_topics or im_messages -- unfriending must not delete history
// (see InternalContactSync's doc comment), but it must revoke the
// authorization gate that im_topic_members actually is. resolveP2PTopic
// treats "the caller currently has a membership row" as proof of an
// already-verified friendship and skips the AreFriends round trip on that
// basis; leaving these rows in place after an unfriend/reject/block event
// would make that skip permanent, letting a removed contact keep messaging,
// reading full history, and even hard-deleting the shared conversation via
// del_topic. Idempotent: safe to call even if the pair never had a topic.
func (s *Store) RemoveP2PMembers(topic, uidA, uidB string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, uid := range []string{uidA, uidB} {
		if _, err := tx.Exec(`DELETE FROM im_topic_members WHERE topic = $1 AND uid = $2`, topic, uid); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM im_pending_clears WHERE topic = $1 AND uid = $2`, topic, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IsGroupMember checks if a user is a member of a group
func (s *Store) IsGroupMember(topic, uid string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM im_topic_members WHERE topic = $1 AND uid = $2`, topic, uid).Scan(&count)
	return count > 0, err
}

// IsGroupOwner checks if a user is the owner of a group
func (s *Store) IsGroupOwner(topic, uid string) (bool, error) {
	var ownerUID string
	err := s.db.QueryRow(`SELECT owner_uid FROM im_topics WHERE topic = $1`, topic).Scan(&ownerUID)
	if err != nil {
		return false, err
	}
	return ownerUID == uid, nil
}

// GetMemberRole returns this uid's role in the topic, or ("", nil) when they
// are not a member at all. The no-row case is deliberately NOT an error:
// "not a member" is a perfectly ordinary answer to this question and every
// caller has to handle it anyway, so making it look like a database failure
// only tempts callers into treating a genuine DB outage as "just not a
// member" (fail-open on an authorization check).
func (s *Store) GetMemberRole(topic, uid string) (string, error) {
	var role string
	err := s.db.QueryRow(
		`SELECT role FROM im_topic_members WHERE topic = $1 AND uid = $2`,
		topic, uid,
	).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

// CanManageGroup answers the one question every group-management entry point
// (kick, update name, clear conversation, ...) needs: may this uid act on
// this group? True for the owner and for admins.
//
// The owner arm checks im_topics.owner_uid rather than the membership row's
// role, and does so as a plain OR alongside the role check, on purpose:
// those two sources of truth can disagree (that is precisely what F-22's
// silent demotion produced), and a management check should recognize the
// owner if EITHER says so. IsGroupOwner keeps using owner_uid alone as the
// single strict definition of ownership for the operations only an owner may
// perform.
//
// Errors are propagated, never swallowed into false: a caller that cannot
// tell "not allowed" from "database unreachable" will eventually be written
// to fail one of them the wrong way.
func (s *Store) CanManageGroup(topic, uid string) (bool, error) {
	var can bool
	err := s.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM im_topics WHERE topic = $1 AND owner_uid = $2 AND owner_uid <> ''
		) OR EXISTS(
			SELECT 1 FROM im_topic_members WHERE topic = $1 AND uid = $2 AND role = $3
		)
	`, topic, uid, RoleAdmin).Scan(&can)
	if err != nil {
		return false, err
	}
	return can, nil
}

// SetGroupMemberRole promotes a member to admin or demotes an admin back to
// member. It refuses to write anything other than those two roles, and never
// touches the owner's row.
//
// Two guards, both load-bearing:
//
//   - The Go-side whitelist. role reaches here from a client-supplied
//     `memberRole` field; without it a client could write role = 'owner' (or
//     any junk string) into a membership row, producing a second owner row
//     that disagrees with im_topics.owner_uid. Ownership moves only through
//     TransferGroupOwnership, which updates both places in one transaction.
//   - `AND role <> RoleOwner` in the SQL. Even a correct caller can race:
//     ownership can transfer between the caller's permission check and this
//     UPDATE. Without this predicate that race demotes the sitting owner to
//     member while im_topics.owner_uid still names them -- a group whose
//     owner is not an owner.
//
// A zero-row result is an error rather than a silent success: it means the
// target is not a member, or is the owner, or the group is gone. Reporting
// that as success would show the caller's UI a promotion that never happened.
func (s *Store) SetGroupMemberRole(topic, uid, role string) error {
	if role != RoleAdmin && role != RoleMember {
		return fmt.Errorf("set member role: refusing to assign role %q", role)
	}
	res, err := s.db.Exec(`
		UPDATE im_topic_members SET role = $3
		WHERE topic = $1 AND uid = $2 AND role <> $4
	`, topic, uid, role, RoleOwner)
	if err != nil {
		return fmt.Errorf("set member role: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set member role: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("set member role: %s is not an assignable member of %s", uid, topic)
	}
	return nil
}

// CountGroupAdmins returns how many admin rows a group currently has, so the
// promote path can enforce a ceiling. The owner is not counted -- they are
// not an admin row and cannot be demoted, so including them would make the
// configured limit mean one fewer actual admin than it says.
func (s *Store) CountGroupAdmins(topic string) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM im_topic_members WHERE topic = $1 AND role = $2`,
		topic, RoleAdmin,
	).Scan(&n)
	return n, err
}

// IsGroupTopic reports whether a topic exists and is a group.
//
// A missing topic answers (false, nil) -- same reasoning as GetMemberRole's
// no-row case. Callers use this to reject group-only actions on P2P topics;
// the `grp_` prefix is a naming convention, this is the stored fact, and
// authorization decisions should rest on the latter.
func (s *Store) IsGroupTopic(topic string) (bool, error) {
	var isGroup bool
	err := s.db.QueryRow(`SELECT is_group FROM im_topics WHERE topic = $1`, topic).Scan(&isGroup)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return isGroup, nil
}

// UpdateGroupName updates the group display name
func (s *Store) UpdateGroupName(topic, name string) error {
	_, err := s.db.Exec(`UPDATE im_topics SET name = $1, updated_at = NOW() WHERE topic = $2`, name, topic)
	return err
}

// TransferGroupOwnership hands the owner role from fromUID to toUID. The
// WHERE owner_uid = fromUID guard makes this atomically safe even if the
// caller's earlier IsGroupOwner check has since gone stale (0 rows affected
// means fromUID is no longer owner).
func (s *Store) TransferGroupOwnership(topic, fromUID, toUID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE im_topics SET owner_uid = $1, updated_at = NOW() WHERE topic = $2 AND owner_uid = $3`, toUID, topic, fromUID)
	if err != nil {
		return fmt.Errorf("transfer ownership: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("transfer ownership: %s is not the current owner of %s", fromUID, topic)
	}

	// D3: the outgoing owner drops all the way to plain member, not to admin.
	// Handing over ownership is meant to be a real hand-off of control; if the
	// old owner kept management rights, "转让群主" would not actually give
	// anything up. The new owner can promote them back via set_member_role if
	// that is what both sides want.
	if _, err := tx.Exec(`UPDATE im_topic_members SET role = $3 WHERE topic = $1 AND uid = $2`, topic, fromUID, RoleMember); err != nil {
		return fmt.Errorf("demote old owner: %w", err)
	}
	if _, err := tx.Exec(`UPDATE im_topic_members SET role = $3 WHERE topic = $1 AND uid = $2`, topic, toUID, RoleOwner); err != nil {
		return fmt.Errorf("promote new owner: %w", err)
	}

	return tx.Commit()
}

// DisbandGroup removes every member of a group topic. im_topics/im_messages
// rows are intentionally kept (message history stays intact for anyone with
// a local cache), but with no members left, IsGroupMember/IsGroupOwner/
// GetUserGroups/GetUserTopics/handlePub's membership check all naturally
// stop recognizing the topic for everyone — no separate "disbanded" flag
// needed.
//
// F-21: two things this used to leave behind, both consequences of the
// deliberate decision to keep the im_topics row.
//
//  1. im_group_settings. Its ON DELETE CASCADE hangs off im_topics(topic),
//     which is exactly the row we do NOT delete — so the cascade never fires
//     and the group's AI-QA config (enabled flag, knowledge-base ids, quota)
//     outlived the group forever. The comment on that table's DDL asserted
//     the opposite; it has been corrected.
//  2. im_topics.owner_uid. Left pointing at the ex-owner even though their
//     membership row is gone, so IsGroupOwner("disbanded topic", oldOwner)
//     still answers true while IsGroupMember answers false — two
//     authorization helpers disagreeing about a dead group. Resetting it to
//     the column's own empty-string default makes every owner check fail
//     closed.
//
// Deliberately NOT deleting the im_topics row itself: it also carries
// last_seq, and dropping it would let GetNextSeq restart this topic's seq
// series at 1 while clients still hold cached messages at higher seqs.
func (s *Store) DisbandGroup(topic string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("disband group: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM im_topic_members WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM im_pending_clears WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM im_group_settings WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group (settings): %w", err)
	}
	// Same F-21 shape as im_group_settings above: im_group_invite_tokens' and
	// im_group_join_requests' ON DELETE CASCADE / lack thereof both hang off
	// im_topics(topic), which this function deliberately never deletes -- so
	// both must be cleaned up explicitly or a disbanded group's QR token
	// stays scannable and its stale join requests stay listable forever.
	if _, err := tx.Exec(`DELETE FROM im_group_invite_tokens WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group (invite tokens): %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM im_group_join_requests WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group (join requests): %w", err)
	}
	if _, err := tx.Exec(`UPDATE im_topics SET owner_uid = '', updated_at = NOW() WHERE topic = $1`, topic); err != nil {
		return fmt.Errorf("disband group (clear owner): %w", err)
	}
	// Queued "go refresh this group" nudges and queued "someone applied to
	// join" notices for a group that no longer exists are both dead weight —
	// the grp_disband event the caller enqueues right after this supersedes
	// them. Scoped to these two kinds precisely so it can never eat that
	// disband row itself, whichever order the two happen in.
	if _, err := tx.Exec(
		`DELETE FROM im_pending_group_events WHERE topic = $1 AND kind IN ($2, $3)`,
		topic, PendingGroupEventSync, PendingGroupEventJoinRequest,
	); err != nil {
		return fmt.Errorf("disband group (pending events): %w", err)
	}
	return tx.Commit()
}

// GetUserGroups returns all groups a user belongs to
func (s *Store) GetUserGroups(uid string) ([]GroupInfo, error) {
	rows, err := s.db.Query(`
		SELECT t.topic, t.name, t.owner_uid, t.encrypted,
			(SELECT COUNT(*) FROM im_topic_members m WHERE m.topic = t.topic)
		FROM im_topics t
		JOIN im_topic_members m ON t.topic = m.topic
		WHERE m.uid = $1 AND t.is_group = true
		ORDER BY t.updated_at DESC
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []GroupInfo
	for rows.Next() {
		var g GroupInfo
		if err := rows.Scan(&g.Topic, &g.Name, &g.Owner, &g.Encrypted, &g.Members); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// GetP2PTopic returns a deterministic topic for two-user conversation
// Topic format: p2p_<sorted(uidA, uidB) hash>
func (s *Store) GetP2PTopic(uidA, uidB string) string {
	pair := []string{uidA, uidB}
	sort.Strings(pair)
	key := pair[0] + ":" + pair[1]
	h := sha256.Sum256([]byte(key))
	return "p2p_" + base64url(h[:8])
}

// EnsureP2PTopic creates a P2P topic if it doesn't exist and adds both users as members
func (s *Store) EnsureP2PTopic(uidA, uidB string) (string, error) {
	topic := s.GetP2PTopic(uidA, uidB)

	// Create topic if not exists
	_, err := s.db.Exec(`
		INSERT INTO im_topics (topic, is_group, name, last_seq)
		VALUES ($1, false, '', 0)
		ON CONFLICT (topic) DO NOTHING
	`, topic)
	if err != nil {
		return topic, fmt.Errorf("ensure p2p topic: %w", err)
	}

	// Add both users as members
	for _, uid := range []string{uidA, uidB} {
		_, err = s.db.Exec(`
			INSERT INTO im_topic_members (topic, uid, role)
			VALUES ($1, $2, $3)
			ON CONFLICT (topic, uid) DO NOTHING
		`, topic, uid, RoleMember)
		if err != nil {
			return topic, fmt.Errorf("add member to p2p: %w", err)
		}
	}

	return topic, nil
}

// GetTopicMemberUIDs returns all member UIDs for a topic (P2P or group) —
// used to find offline recipients for push notifications.
func (s *Store) GetTopicMemberUIDs(topic string) ([]string, error) {
	rows, err := s.db.Query(`SELECT uid FROM im_topic_members WHERE topic = $1`, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uids []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		uids = append(uids, uid)
	}
	return uids, nil
}

// GetGroupManagerUIDs returns a group's owner and all current admins --
// exactly the set authorized by ensureGroupManager, used to fan out
// join-request review notifications to everyone who can act on one.
func (s *Store) GetGroupManagerUIDs(topic string) ([]string, error) {
	rows, err := s.db.Query(`SELECT uid FROM im_topic_members WHERE topic = $1 AND role IN ($2, $3)`, topic, RoleOwner, RoleAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uids []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		uids = append(uids, uid)
	}
	return uids, rows.Err()
}
