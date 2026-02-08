-- v30 -> v31: rename ai_cron_state to ai_bridge_state (generic bridge-internal KV store)
ALTER TABLE ai_cron_state RENAME TO ai_bridge_state;
