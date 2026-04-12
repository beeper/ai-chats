-- v0 -> v1: create canonical AI Chats schema
-- Canonical initial schema for fresh AI Chats databases.
CREATE TABLE IF NOT EXISTS aichats_memory_files (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  path TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'memory',
  content TEXT NOT NULL,
  hash TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (bridge_id, login_id, agent_id, path)
);

CREATE TABLE IF NOT EXISTS aichats_memory_chunks (
  id TEXT PRIMARY KEY,
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  path TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'memory',
  start_line INTEGER NOT NULL,
  end_line INTEGER NOT NULL,
  hash TEXT NOT NULL,
  model TEXT NOT NULL,
  text TEXT NOT NULL,
  embedding TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_aichats_memory_chunks_lookup ON aichats_memory_chunks(bridge_id, login_id, agent_id, model, source);
CREATE INDEX IF NOT EXISTS idx_aichats_memory_chunks_path ON aichats_memory_chunks(path);

CREATE TABLE IF NOT EXISTS aichats_memory_meta (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  provider_key TEXT NOT NULL,
  chunk_tokens INTEGER NOT NULL,
  chunk_overlap INTEGER NOT NULL,
  vector_dims INTEGER,
  index_generation TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (bridge_id, login_id, agent_id)
);

CREATE TABLE IF NOT EXISTS aichats_memory_embedding_cache (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  provider_key TEXT NOT NULL,
  hash TEXT NOT NULL,
  embedding TEXT NOT NULL,
  dims INTEGER,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (bridge_id, login_id, agent_id, provider, model, provider_key, hash)
);

CREATE INDEX IF NOT EXISTS idx_aichats_memory_embedding_cache_updated_at ON aichats_memory_embedding_cache(updated_at);

CREATE TABLE IF NOT EXISTS aichats_memory_session_state (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  session_key TEXT NOT NULL,
  content_hash TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (bridge_id, login_id, agent_id, session_key)
);

CREATE TABLE IF NOT EXISTS aichats_memory_session_files (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  session_key TEXT NOT NULL,
  path TEXT NOT NULL,
  content TEXT NOT NULL,
  hash TEXT NOT NULL,
  size INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (bridge_id, login_id, agent_id, session_key, path)
);

CREATE INDEX IF NOT EXISTS idx_aichats_memory_session_files_path ON aichats_memory_session_files(path);

CREATE TABLE IF NOT EXISTS aichats_cron_jobs (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  job_id TEXT NOT NULL,
  agent_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  delete_after_run INTEGER NOT NULL DEFAULT 0,
  created_at_ms INTEGER NOT NULL DEFAULT 0,
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  schedule_kind TEXT NOT NULL DEFAULT '',
  schedule_at TEXT NOT NULL DEFAULT '',
  schedule_every_ms INTEGER,
  schedule_anchor_ms INTEGER,
  schedule_expr TEXT NOT NULL DEFAULT '',
  schedule_tz TEXT NOT NULL DEFAULT '',
  payload_kind TEXT NOT NULL DEFAULT '',
  payload_message TEXT NOT NULL DEFAULT '',
  payload_model TEXT NOT NULL DEFAULT '',
  payload_thinking TEXT NOT NULL DEFAULT '',
  payload_timeout_seconds INTEGER,
  payload_allow_unsafe_external INTEGER,
  delivery_mode TEXT NOT NULL DEFAULT '',
  delivery_channel TEXT NOT NULL DEFAULT '',
  delivery_to TEXT NOT NULL DEFAULT '',
  delivery_best_effort INTEGER,
  state_next_run_at_ms INTEGER,
  state_running_at_ms INTEGER,
  state_last_run_at_ms INTEGER,
  state_last_status TEXT NOT NULL DEFAULT '',
  state_last_error TEXT NOT NULL DEFAULT '',
  state_last_duration_ms INTEGER,
  room_id TEXT NOT NULL DEFAULT '',
  revision INTEGER NOT NULL DEFAULT 1,
  pending_run_key TEXT NOT NULL DEFAULT '',
  last_output_preview TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (bridge_id, login_id, job_id)
);

CREATE INDEX IF NOT EXISTS idx_aichats_cron_jobs_lookup ON aichats_cron_jobs(bridge_id, login_id, agent_id);

CREATE TABLE IF NOT EXISTS aichats_cron_job_run_keys (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  job_id TEXT NOT NULL,
  run_index INTEGER NOT NULL,
  run_key TEXT NOT NULL,
  PRIMARY KEY (bridge_id, login_id, job_id, run_index),
  UNIQUE (bridge_id, login_id, job_id, run_key)
);

CREATE TABLE IF NOT EXISTS aichats_managed_heartbeats (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  interval_ms INTEGER NOT NULL DEFAULT 0,
  active_hours_start TEXT NOT NULL DEFAULT '',
  active_hours_end TEXT NOT NULL DEFAULT '',
  active_hours_timezone TEXT NOT NULL DEFAULT '',
  room_id TEXT NOT NULL DEFAULT '',
  revision INTEGER NOT NULL DEFAULT 1,
  next_run_at_ms INTEGER,
  pending_run_key TEXT NOT NULL DEFAULT '',
  last_run_at_ms INTEGER,
  last_result TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (bridge_id, login_id, agent_id)
);

CREATE TABLE IF NOT EXISTS aichats_managed_heartbeat_run_keys (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  run_index INTEGER NOT NULL,
  run_key TEXT NOT NULL,
  PRIMARY KEY (bridge_id, login_id, agent_id, run_index),
  UNIQUE (bridge_id, login_id, agent_id, run_key)
);

CREATE TABLE IF NOT EXISTS aichats_system_events (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL DEFAULT 'beep',
  session_key TEXT NOT NULL,
  event_index INTEGER NOT NULL,
  text TEXT NOT NULL DEFAULT '',
  ts INTEGER NOT NULL DEFAULT 0,
  last_text TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (bridge_id, login_id, agent_id, session_key, event_index)
);

CREATE TABLE IF NOT EXISTS aichats_internal_messages (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  portal_id TEXT NOT NULL,
  event_id TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  canonical_turn_data TEXT NOT NULL DEFAULT '',
  exclude_from_history INTEGER NOT NULL DEFAULT 0,
  created_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, login_id, portal_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_aichats_internal_messages_history
  ON aichats_internal_messages(bridge_id, login_id, portal_id, created_at_ms);

CREATE TABLE IF NOT EXISTS aichats_login_state (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  next_chat_index INTEGER NOT NULL DEFAULT 0,
  last_heartbeat_event_json TEXT NOT NULL DEFAULT '',
  model_cache_json TEXT NOT NULL DEFAULT '',
  gravatar_json TEXT NOT NULL DEFAULT '',
  file_annotation_cache_json TEXT NOT NULL DEFAULT '',
  consecutive_errors INTEGER NOT NULL DEFAULT 0,
  last_error_at INTEGER NOT NULL DEFAULT 0,
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, login_id)
);

CREATE TABLE IF NOT EXISTS aichats_login_config (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  config_json TEXT NOT NULL DEFAULT '',
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, login_id)
);

CREATE TABLE IF NOT EXISTS aichats_custom_agents (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  content_json TEXT NOT NULL DEFAULT '',
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, login_id, agent_id)
);

CREATE TABLE IF NOT EXISTS aichats_portal_state (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  portal_id TEXT NOT NULL,
  state_json TEXT NOT NULL DEFAULT '',
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, login_id, portal_id)
);

CREATE TABLE IF NOT EXISTS aichats_tool_approval_rules (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  tool_kind TEXT NOT NULL,
  server_label TEXT NOT NULL DEFAULT '',
  tool_name TEXT NOT NULL,
  action TEXT NOT NULL DEFAULT '',
  created_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, login_id, tool_kind, server_label, tool_name, action)
);

CREATE INDEX IF NOT EXISTS idx_aichats_tool_approval_rules_lookup
  ON aichats_tool_approval_rules(bridge_id, login_id, tool_kind, tool_name);

CREATE TABLE IF NOT EXISTS aichats_sessions (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  store_agent_id TEXT NOT NULL,
  session_key TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  last_heartbeat_text TEXT NOT NULL DEFAULT '',
  last_heartbeat_sent_at_ms INTEGER NOT NULL DEFAULT 0,
  last_channel TEXT NOT NULL DEFAULT '',
  last_to TEXT NOT NULL DEFAULT '',
  last_account_id TEXT NOT NULL DEFAULT '',
  last_thread_id TEXT NOT NULL DEFAULT '',
  queue_mode TEXT NOT NULL DEFAULT '',
  queue_debounce_ms INTEGER,
  queue_cap INTEGER,
  queue_drop TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (bridge_id, login_id, store_agent_id, session_key)
);

CREATE INDEX IF NOT EXISTS idx_aichats_sessions_lookup
  ON aichats_sessions(bridge_id, login_id, store_agent_id);

CREATE INDEX IF NOT EXISTS idx_aichats_sessions_updated
  ON aichats_sessions(bridge_id, login_id, store_agent_id, updated_at_ms);

CREATE TABLE IF NOT EXISTS aichats_transcript_messages (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  portal_id TEXT NOT NULL,
  message_id TEXT NOT NULL,
  event_id TEXT NOT NULL DEFAULT '',
  sender_id TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '',
  created_at_ms INTEGER NOT NULL DEFAULT 0,
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, login_id, portal_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_aichats_transcript_portal
  ON aichats_transcript_messages(bridge_id, login_id, portal_id, created_at_ms);
