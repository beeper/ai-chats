-- v0 -> v1: create canonical AgentRemote schema
-- Canonical initial schema for fresh databases.
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
  last_rowid INTEGER NOT NULL DEFAULT 0,
  pending_bytes INTEGER NOT NULL DEFAULT 0,
  pending_messages INTEGER NOT NULL DEFAULT 0,
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
  pending_delay_id TEXT NOT NULL DEFAULT '',
  pending_delay_kind TEXT NOT NULL DEFAULT '',
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
  pending_delay_id TEXT NOT NULL DEFAULT '',
  pending_delay_kind TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS agentremote_sessions (
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

CREATE INDEX IF NOT EXISTS idx_agentremote_sessions_lookup
  ON agentremote_sessions(bridge_id, login_id, store_agent_id);

CREATE INDEX IF NOT EXISTS idx_agentremote_sessions_updated
  ON agentremote_sessions(bridge_id, login_id, store_agent_id, updated_at_ms);

CREATE TABLE IF NOT EXISTS agentremote_approvals (
  bridge_id TEXT NOT NULL,
  login_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  approval_id TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT '',
  room_id TEXT NOT NULL DEFAULT '',
  turn_id TEXT NOT NULL DEFAULT '',
  tool_call_id TEXT NOT NULL DEFAULT '',
  tool_name TEXT NOT NULL DEFAULT '',
  request_json TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  expires_at_ms INTEGER NOT NULL DEFAULT 0,
  created_at_ms INTEGER NOT NULL DEFAULT 0,
  updated_at_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bridge_id, login_id, agent_id, approval_id)
);

CREATE INDEX IF NOT EXISTS idx_agentremote_approvals_lookup
  ON agentremote_approvals(bridge_id, login_id, agent_id, status, expires_at_ms);
