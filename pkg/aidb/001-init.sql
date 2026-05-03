-- v0 -> v1: create canonical AI Chats schema
-- Canonical initial schema for fresh AI Chats databases.
CREATE TABLE IF NOT EXISTS aichats_login_state (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  next_chat_index INTEGER NOT NULL DEFAULT 0,
  model_cache_json TEXT NOT NULL DEFAULT '{}',
  file_annotation_cache_json TEXT NOT NULL DEFAULT '{}',
  consecutive_errors INTEGER NOT NULL DEFAULT 0,
  last_error_at INTEGER NOT NULL DEFAULT 0,
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, login_id)
);

CREATE TABLE IF NOT EXISTS aichats_portal_state (
  bridge_id TEXT NOT NULL,
  portal_id TEXT NOT NULL,
  portal_receiver TEXT NOT NULL,
  context_epoch INTEGER NOT NULL DEFAULT 0,
  next_turn_sequence INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, portal_id, portal_receiver)
);

CREATE TABLE IF NOT EXISTS ai_conversation_state (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  portal_id TEXT NOT NULL,
  state_json TEXT NOT NULL DEFAULT '',
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, login_id, portal_id)
);

CREATE TABLE IF NOT EXISTS aichats_turns (
  bridge_id TEXT NOT NULL,
  portal_id TEXT NOT NULL,
  portal_receiver TEXT NOT NULL,
  turn_id TEXT NOT NULL,
  context_epoch INTEGER NOT NULL DEFAULT 0,
  sequence INTEGER NOT NULL,
  kind TEXT NOT NULL DEFAULT 'conversation',
  source TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT '',
  sender_id TEXT NOT NULL DEFAULT '',
  include_in_history BOOLEAN NOT NULL DEFAULT true,
  turn_data_json TEXT NOT NULL DEFAULT '{}',
  meta_json TEXT NOT NULL DEFAULT '{}',
  created_at_ms INTEGER NOT NULL DEFAULT 0,
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, portal_id, portal_receiver, turn_id),
  UNIQUE (bridge_id, portal_id, portal_receiver, context_epoch, sequence)
);

CREATE INDEX IF NOT EXISTS idx_aichats_turns_history
  ON aichats_turns(bridge_id, portal_id, portal_receiver, context_epoch, sequence DESC);

CREATE INDEX IF NOT EXISTS idx_aichats_turns_role
  ON aichats_turns(bridge_id, portal_id, portal_receiver, role, include_in_history, sequence DESC);

CREATE TABLE IF NOT EXISTS aichats_turn_refs (
  bridge_id TEXT NOT NULL,
  portal_id TEXT NOT NULL,
  portal_receiver TEXT NOT NULL,
  ref_kind TEXT NOT NULL,
  ref_value TEXT NOT NULL,
  turn_id TEXT NOT NULL,
  PRIMARY KEY (bridge_id, portal_id, portal_receiver, ref_kind, ref_value)
);

CREATE INDEX IF NOT EXISTS idx_aichats_turn_refs_turn
  ON aichats_turn_refs(bridge_id, portal_id, portal_receiver, turn_id);
