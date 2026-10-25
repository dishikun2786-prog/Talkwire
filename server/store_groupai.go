// Store: group AI settings (optional module).

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// GroupAiSettings is the per-group configuration for the "@小桃子" group QA
// feature (see im_group_settings). AiDailyQuota/AiCooldownSec are ops-level
// knobs with column defaults; the update_group_ai_settings WS action only
// lets the group owner edit AiQaEnabled/AiKbIds/AiDocIds (see
// UpsertGroupAiSettings).
type GroupAiSettings struct {
	Topic         string
	AiQaEnabled   bool
	AiKbIds       []string
	AiDocIds      []string
	AiDailyQuota  int
	AiCooldownSec int
}

// defaultGroupAiSettings mirrors im_group_settings' column defaults, used for
// the (overwhelmingly common) case of a group that has never had its AI QA
// settings touched and therefore has no row yet.
func defaultGroupAiSettings(topic string) *GroupAiSettings {
	return &GroupAiSettings{
		Topic: topic, AiQaEnabled: false,
		AiKbIds: []string{}, AiDocIds: []string{},
		AiDailyQuota: 50, AiCooldownSec: 5,
	}
}

// GetGroupAiSettings reads a group's AI QA config, or the documented defaults
// if the group has never configured it. Callers needing repeated reads
// should go through Hub.GetGroupAiSettingsCached instead of hitting this
// directly on every message.
func (s *Store) GetGroupAiSettings(topic string) (*GroupAiSettings, error) {
	var g GroupAiSettings
	var kbIds, docIds []byte
	err := s.db.QueryRow(`
		SELECT topic, ai_qa_enabled, ai_kb_ids, ai_doc_ids, ai_daily_quota, ai_cooldown_sec
		FROM im_group_settings WHERE topic = $1
	`, topic).Scan(&g.Topic, &g.AiQaEnabled, &kbIds, &docIds, &g.AiDailyQuota, &g.AiCooldownSec)
	if err == sql.ErrNoRows {
		return defaultGroupAiSettings(topic), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get group ai settings: %w", err)
	}
	if err := json.Unmarshal(kbIds, &g.AiKbIds); err != nil || g.AiKbIds == nil {
		g.AiKbIds = []string{}
	}
	if err := json.Unmarshal(docIds, &g.AiDocIds); err != nil || g.AiDocIds == nil {
		g.AiDocIds = []string{}
	}
	return &g, nil
}

// UpsertGroupAiSettings creates or updates a group's AI QA configuration.
// updatedBy is the caller's uid; the WS handler (handleUpdateGroupAiSettings)
// must already have verified that uid is the group owner before calling
// this — this method itself does not re-check ownership.
func (s *Store) UpsertGroupAiSettings(topic string, enabled bool, kbIds, docIds []string, updatedBy string) error {
	if kbIds == nil {
		kbIds = []string{}
	}
	if docIds == nil {
		docIds = []string{}
	}
	kbJSON, err := json.Marshal(kbIds)
	if err != nil {
		return fmt.Errorf("marshal ai_kb_ids: %w", err)
	}
	docJSON, err := json.Marshal(docIds)
	if err != nil {
		return fmt.Errorf("marshal ai_doc_ids: %w", err)
	}
	// ai_daily_quota/ai_cooldown_sec are deliberately left out of the SET
	// clause: this action only lets a group owner edit enabled/kbIds/docIds,
	// not the ops-level quota/cooldown, and ON CONFLICT DO UPDATE without
	// naming a column leaves it unchanged.
	_, err = s.db.Exec(`
		INSERT INTO im_group_settings (topic, ai_qa_enabled, ai_kb_ids, ai_doc_ids, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (topic) DO UPDATE SET
			ai_qa_enabled = EXCLUDED.ai_qa_enabled,
			ai_kb_ids = EXCLUDED.ai_kb_ids,
			ai_doc_ids = EXCLUDED.ai_doc_ids,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
	`, topic, enabled, kbJSON, docJSON, updatedBy)
	if err != nil {
		return fmt.Errorf("upsert group ai settings: %w", err)
	}
	return nil
}
