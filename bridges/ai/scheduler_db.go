package ai

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"go.mau.fi/util/dbutil"

	integrationcron "github.com/beeper/agentremote/pkg/integrations/cron"
)

type schedulerDBScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

func (s *schedulerRuntime) schedulerDBScope() *schedulerDBScope {
	if s == nil || s.client == nil || s.client.UserLogin == nil || s.client.UserLogin.Bridge == nil || s.client.UserLogin.Bridge.DB == nil {
		return nil
	}
	db := s.client.bridgeDB()
	if db == nil {
		return nil
	}
	return &schedulerDBScope{
		db:       db,
		bridgeID: string(s.client.UserLogin.Bridge.DB.BridgeID),
		loginID:  string(s.client.UserLogin.ID),
	}
}

func (s *schedulerRuntime) loadCronStoreLocked(ctx context.Context) (scheduledCronStore, error) {
	scope := s.schedulerDBScope()
	if scope == nil {
		return scheduledCronStore{}, nil
	}
	rows, err := scope.db.Query(ctx, `
		SELECT
			job_id, agent_id, name, description, enabled, delete_after_run,
			created_at_ms, updated_at_ms,
			schedule_kind, schedule_at, schedule_every_ms, schedule_anchor_ms, schedule_expr, schedule_tz,
			payload_kind, payload_message, payload_model, payload_thinking, payload_timeout_seconds, payload_allow_unsafe_external,
			delivery_mode, delivery_channel, delivery_to, delivery_best_effort,
			state_next_run_at_ms, state_running_at_ms, state_last_run_at_ms, state_last_status, state_last_error, state_last_duration_ms,
			room_id, revision, pending_run_key, last_output_preview
		FROM aichats_cron_jobs
		WHERE bridge_id=$1 AND login_id=$2
		ORDER BY job_id
	`, scope.bridgeID, scope.loginID)
	if err != nil {
		return scheduledCronStore{}, err
	}
	defer rows.Close()

	store := scheduledCronStore{}
	for rows.Next() {
		var (
			record              scheduledCronJob
			enabled             bool
			deleteAfterRun      bool
			scheduleEveryMs     sql.NullInt64
			scheduleAnchorMs    sql.NullInt64
			payloadTimeout      sql.NullInt64
			payloadAllowUnsafe  sql.NullBool
			deliveryMode        string
			deliveryChannel     string
			deliveryTo          string
			deliveryBestEffort  sql.NullBool
			stateNextRunAtMs    sql.NullInt64
			stateRunningAtMs    sql.NullInt64
			stateLastRunAtMs    sql.NullInt64
			stateLastDurationMs sql.NullInt64
		)
		if err := rows.Scan(
			&record.Job.ID,
			&record.Job.AgentID,
			&record.Job.Name,
			&record.Job.Description,
			&enabled,
			&deleteAfterRun,
			&record.Job.CreatedAtMs,
			&record.Job.UpdatedAtMs,
			&record.Job.Schedule.Kind,
			&record.Job.Schedule.At,
			&scheduleEveryMs,
			&scheduleAnchorMs,
			&record.Job.Schedule.Expr,
			&record.Job.Schedule.TZ,
			&record.Job.Payload.Kind,
			&record.Job.Payload.Message,
			&record.Job.Payload.Model,
			&record.Job.Payload.Thinking,
			&payloadTimeout,
			&payloadAllowUnsafe,
			&deliveryMode,
			&deliveryChannel,
			&deliveryTo,
			&deliveryBestEffort,
			&stateNextRunAtMs,
			&stateRunningAtMs,
			&stateLastRunAtMs,
			&record.Job.State.LastStatus,
			&record.Job.State.LastError,
			&stateLastDurationMs,
			&record.RoomID,
			&record.Revision,
			&record.PendingRunKey,
			&record.LastOutputPreview,
		); err != nil {
			return scheduledCronStore{}, err
		}
		record.Job.Enabled = enabled
		record.Job.DeleteAfterRun = deleteAfterRun
		record.Job.Schedule.EveryMs = scheduleEveryMs.Int64
		record.Job.Schedule.AnchorMs = nullableInt64Pointer(scheduleAnchorMs)
		record.Job.Payload.TimeoutSeconds = nullableIntPointer(payloadTimeout)
		record.Job.Payload.AllowUnsafeExternal = nullableBoolPointer(payloadAllowUnsafe)
		record.Job.State.NextRunAtMs = nullableInt64Pointer(stateNextRunAtMs)
		record.Job.State.RunningAtMs = nullableInt64Pointer(stateRunningAtMs)
		record.Job.State.LastRunAtMs = nullableInt64Pointer(stateLastRunAtMs)
		record.Job.State.LastDurationMs = nullableInt64Pointer(stateLastDurationMs)
		record.Job.Delivery = buildCronDelivery(deliveryMode, deliveryChannel, deliveryTo, deliveryBestEffort)
		record.ProcessedRunKeys, err = loadCronRunKeys(ctx, scope, record.Job.ID)
		if err != nil {
			return scheduledCronStore{}, err
		}
		store.Jobs = append(store.Jobs, record)
	}
	if err := rows.Err(); err != nil {
		return scheduledCronStore{}, err
	}
	return store, nil
}

func (s *schedulerRuntime) saveCronStoreLocked(ctx context.Context, store scheduledCronStore) error {
	scope := s.schedulerDBScope()
	if scope == nil {
		return nil
	}
	return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		keep := make(map[string]struct{}, len(store.Jobs))
		for _, record := range store.Jobs {
			keep[strings.TrimSpace(record.Job.ID)] = struct{}{}
		}
		if err := deleteMissingCronRows(ctx, scope, keep); err != nil {
			return err
		}
		for _, record := range store.Jobs {
			deliveryMode, deliveryChannel, deliveryTo, deliveryBestEffort := flattenCronDelivery(record.Job.Delivery)
			if _, err := scope.db.Exec(ctx, `
				INSERT INTO aichats_cron_jobs (
					bridge_id, login_id, job_id, agent_id, name, description,
					enabled, delete_after_run, created_at_ms, updated_at_ms,
					schedule_kind, schedule_at, schedule_every_ms, schedule_anchor_ms, schedule_expr, schedule_tz,
					payload_kind, payload_message, payload_model, payload_thinking, payload_timeout_seconds, payload_allow_unsafe_external,
					delivery_mode, delivery_channel, delivery_to, delivery_best_effort,
					state_next_run_at_ms, state_running_at_ms, state_last_run_at_ms, state_last_status, state_last_error, state_last_duration_ms,
					room_id, revision, pending_run_key, last_output_preview
				) VALUES (
					$1, $2, $3, $4, $5, $6,
					$7, $8, $9, $10,
					$11, $12, $13, $14, $15, $16,
					$17, $18, $19, $20, $21, $22,
					$23, $24, $25, $26,
					$27, $28, $29, $30, $31, $32,
					$33, $34, $35, $36
				)
				ON CONFLICT (bridge_id, login_id, job_id) DO UPDATE SET
					agent_id=excluded.agent_id,
					name=excluded.name,
					description=excluded.description,
					enabled=excluded.enabled,
					delete_after_run=excluded.delete_after_run,
					created_at_ms=excluded.created_at_ms,
					updated_at_ms=excluded.updated_at_ms,
					schedule_kind=excluded.schedule_kind,
					schedule_at=excluded.schedule_at,
					schedule_every_ms=excluded.schedule_every_ms,
					schedule_anchor_ms=excluded.schedule_anchor_ms,
					schedule_expr=excluded.schedule_expr,
					schedule_tz=excluded.schedule_tz,
					payload_kind=excluded.payload_kind,
					payload_message=excluded.payload_message,
					payload_model=excluded.payload_model,
					payload_thinking=excluded.payload_thinking,
					payload_timeout_seconds=excluded.payload_timeout_seconds,
					payload_allow_unsafe_external=excluded.payload_allow_unsafe_external,
					delivery_mode=excluded.delivery_mode,
					delivery_channel=excluded.delivery_channel,
					delivery_to=excluded.delivery_to,
					delivery_best_effort=excluded.delivery_best_effort,
					state_next_run_at_ms=excluded.state_next_run_at_ms,
					state_running_at_ms=excluded.state_running_at_ms,
					state_last_run_at_ms=excluded.state_last_run_at_ms,
					state_last_status=excluded.state_last_status,
					state_last_error=excluded.state_last_error,
					state_last_duration_ms=excluded.state_last_duration_ms,
					room_id=excluded.room_id,
					revision=excluded.revision,
					pending_run_key=excluded.pending_run_key,
					last_output_preview=excluded.last_output_preview
			`,
				scope.bridgeID, scope.loginID, record.Job.ID, record.Job.AgentID, record.Job.Name, record.Job.Description,
				record.Job.Enabled, record.Job.DeleteAfterRun, record.Job.CreatedAtMs, record.Job.UpdatedAtMs,
				record.Job.Schedule.Kind, record.Job.Schedule.At, nullableInt64ValueForZero(record.Job.Schedule.EveryMs), nullableInt64Value(record.Job.Schedule.AnchorMs), record.Job.Schedule.Expr, record.Job.Schedule.TZ,
				record.Job.Payload.Kind, record.Job.Payload.Message, record.Job.Payload.Model, record.Job.Payload.Thinking, nullableIntValue(record.Job.Payload.TimeoutSeconds), nullableBoolValue(record.Job.Payload.AllowUnsafeExternal),
				deliveryMode, deliveryChannel, deliveryTo, deliveryBestEffort,
				nullableInt64Value(record.Job.State.NextRunAtMs), nullableInt64Value(record.Job.State.RunningAtMs), nullableInt64Value(record.Job.State.LastRunAtMs), record.Job.State.LastStatus, record.Job.State.LastError, nullableInt64Value(record.Job.State.LastDurationMs),
				record.RoomID, record.Revision, record.PendingRunKey, record.LastOutputPreview,
			); err != nil {
				return err
			}
			if err := replaceCronRunKeys(ctx, scope, record.Job.ID, record.ProcessedRunKeys); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *schedulerRuntime) loadHeartbeatStoreLocked(ctx context.Context) (managedHeartbeatStore, error) {
	scope := s.schedulerDBScope()
	if scope == nil {
		return managedHeartbeatStore{}, nil
	}
	rows, err := scope.db.Query(ctx, `
		SELECT
			agent_id, enabled, interval_ms,
			active_hours_start, active_hours_end, active_hours_timezone,
			room_id, revision, next_run_at_ms, pending_run_key,
			last_run_at_ms, last_result, last_error
		FROM aichats_managed_heartbeats
		WHERE bridge_id=$1 AND login_id=$2
		ORDER BY agent_id
	`, scope.bridgeID, scope.loginID)
	if err != nil {
		return managedHeartbeatStore{}, err
	}
	defer rows.Close()

	store := managedHeartbeatStore{}
	for rows.Next() {
		var (
			state          managedHeartbeatState
			enabled        bool
			activeStart    string
			activeEnd      string
			activeTimezone string
			nextRunAtMs    sql.NullInt64
			lastRunAtMs    sql.NullInt64
		)
		if err := rows.Scan(
			&state.AgentID,
			&enabled,
			&state.IntervalMs,
			&activeStart,
			&activeEnd,
			&activeTimezone,
			&state.RoomID,
			&state.Revision,
			&nextRunAtMs,
			&state.PendingRunKey,
			&lastRunAtMs,
			&state.LastResult,
			&state.LastError,
		); err != nil {
			return managedHeartbeatStore{}, err
		}
		state.Enabled = enabled
		state.NextRunAtMs = nextRunAtMs.Int64
		state.LastRunAtMs = lastRunAtMs.Int64
		if strings.TrimSpace(activeStart) != "" || strings.TrimSpace(activeEnd) != "" || strings.TrimSpace(activeTimezone) != "" {
			state.ActiveHours = &HeartbeatActiveHoursConfig{
				Start:    activeStart,
				End:      activeEnd,
				Timezone: activeTimezone,
			}
		}
		state.ProcessedRunKeys, err = loadHeartbeatRunKeys(ctx, scope, state.AgentID)
		if err != nil {
			return managedHeartbeatStore{}, err
		}
		store.Agents = append(store.Agents, state)
	}
	if err := rows.Err(); err != nil {
		return managedHeartbeatStore{}, err
	}
	return store, nil
}

func (s *schedulerRuntime) saveHeartbeatStoreLocked(ctx context.Context, store managedHeartbeatStore) error {
	scope := s.schedulerDBScope()
	if scope == nil {
		return nil
	}
	return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		keep := make(map[string]struct{}, len(store.Agents))
		for _, state := range store.Agents {
			keep[strings.TrimSpace(state.AgentID)] = struct{}{}
		}
		if err := deleteMissingHeartbeatRows(ctx, scope, keep); err != nil {
			return err
		}
		for _, state := range store.Agents {
			activeStart, activeEnd, activeTimezone := flattenHeartbeatActiveHours(state.ActiveHours)
			if _, err := scope.db.Exec(ctx, `
				INSERT INTO aichats_managed_heartbeats (
					bridge_id, login_id, agent_id, enabled, interval_ms,
					active_hours_start, active_hours_end, active_hours_timezone,
					room_id, revision, next_run_at_ms, pending_run_key, last_run_at_ms, last_result, last_error
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
				ON CONFLICT (bridge_id, login_id, agent_id) DO UPDATE SET
					enabled=excluded.enabled,
					interval_ms=excluded.interval_ms,
					active_hours_start=excluded.active_hours_start,
					active_hours_end=excluded.active_hours_end,
					active_hours_timezone=excluded.active_hours_timezone,
					room_id=excluded.room_id,
					revision=excluded.revision,
					next_run_at_ms=excluded.next_run_at_ms,
					pending_run_key=excluded.pending_run_key,
					last_run_at_ms=excluded.last_run_at_ms,
					last_result=excluded.last_result,
					last_error=excluded.last_error
			`,
				scope.bridgeID, scope.loginID, state.AgentID, state.Enabled, state.IntervalMs,
				activeStart, activeEnd, activeTimezone,
				state.RoomID, state.Revision, nullableInt64ValueForZero(state.NextRunAtMs),
				state.PendingRunKey, nullableInt64ValueForZero(state.LastRunAtMs), state.LastResult, state.LastError,
			); err != nil {
				return err
			}
			if err := replaceHeartbeatRunKeys(ctx, scope, state.AgentID, state.ProcessedRunKeys); err != nil {
				return err
			}
		}
		return nil
	})
}

func buildCronDelivery(mode, channel, to string, bestEffort sql.NullBool) *integrationcron.Delivery {
	mode = strings.TrimSpace(mode)
	channel = strings.TrimSpace(channel)
	to = strings.TrimSpace(to)
	if mode == "" && channel == "" && to == "" && !bestEffort.Valid {
		return nil
	}
	delivery := &integrationcron.Delivery{
		Mode:    integrationcron.DeliveryMode(mode),
		Channel: channel,
		To:      to,
	}
	if bestEffort.Valid {
		value := bestEffort.Bool
		delivery.BestEffort = &value
	}
	return delivery
}

func flattenCronDelivery(delivery *integrationcron.Delivery) (string, string, string, any) {
	if delivery == nil {
		return "", "", "", nil
	}
	return string(delivery.Mode), delivery.Channel, delivery.To, nullableBoolValue(delivery.BestEffort)
}

func flattenHeartbeatActiveHours(cfg *HeartbeatActiveHoursConfig) (string, string, string) {
	if cfg == nil {
		return "", "", ""
	}
	return cfg.Start, cfg.End, cfg.Timezone
}

func loadCronRunKeys(ctx context.Context, scope *schedulerDBScope, jobID string) ([]string, error) {
	return loadIndexedRunKeys(ctx, scope, "aichats_cron_job_run_keys", "job_id", jobID)
}

func replaceCronRunKeys(ctx context.Context, scope *schedulerDBScope, jobID string, keys []string) error {
	return replaceIndexedRunKeys(ctx, scope, "aichats_cron_job_run_keys", "job_id", jobID, keys)
}

func loadHeartbeatRunKeys(ctx context.Context, scope *schedulerDBScope, agentID string) ([]string, error) {
	return loadIndexedRunKeys(ctx, scope, "aichats_managed_heartbeat_run_keys", "agent_id", agentID)
}

func replaceHeartbeatRunKeys(ctx context.Context, scope *schedulerDBScope, agentID string, keys []string) error {
	return replaceIndexedRunKeys(ctx, scope, "aichats_managed_heartbeat_run_keys", "agent_id", agentID, keys)
}

func nullableInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}

func nullableIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	v := int(value.Int64)
	return &v
}

func nullableBoolPointer(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	v := value.Bool
	return &v
}

func nullableInt64Value(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt64ValueForZero(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableIntValue(value *int) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func nullableBoolValue(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func deleteMissingCronRows(ctx context.Context, scope *schedulerDBScope, keep map[string]struct{}) error {
	return deleteMissingScopedRows(ctx, scope, keep, "aichats_cron_jobs", "job_id", "aichats_cron_job_run_keys")
}

func deleteMissingHeartbeatRows(ctx context.Context, scope *schedulerDBScope, keep map[string]struct{}) error {
	return deleteMissingScopedRows(ctx, scope, keep, "aichats_managed_heartbeats", "agent_id", "aichats_managed_heartbeat_run_keys")
}

func loadIndexedRunKeys(ctx context.Context, scope *schedulerDBScope, table, idColumn, idValue string) ([]string, error) {
	rows, err := scope.db.Query(ctx, fmt.Sprintf(`
		SELECT run_key
		FROM %s
		WHERE bridge_id=$1 AND login_id=$2 AND %s=$3
		ORDER BY run_index
	`, table, idColumn), scope.bridgeID, scope.loginID, idValue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func replaceIndexedRunKeys(ctx context.Context, scope *schedulerDBScope, table, idColumn, idValue string, keys []string) error {
	if _, err := scope.db.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s
		WHERE bridge_id=$1 AND login_id=$2 AND %s=$3
	`, table, idColumn), scope.bridgeID, scope.loginID, idValue); err != nil {
		return err
	}
	for idx, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, err := scope.db.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s (
				bridge_id, login_id, %s, run_index, run_key
			) VALUES ($1, $2, $3, $4, $5)
		`, table, idColumn), scope.bridgeID, scope.loginID, idValue, idx, key); err != nil {
			return err
		}
	}
	return nil
}

func deleteMissingScopedRows(ctx context.Context, scope *schedulerDBScope, keep map[string]struct{}, entityTable, idColumn, runKeyTable string) error {
	rows, err := scope.db.Query(ctx, fmt.Sprintf(
		`SELECT %s FROM %s WHERE bridge_id=$1 AND login_id=$2`,
		idColumn, entityTable,
	), scope.bridgeID, scope.loginID)
	if err != nil {
		return err
	}
	var toDelete []string
	for rows.Next() {
		var idValue string
		if err := rows.Scan(&idValue); err != nil {
			rows.Close()
			return err
		}
		if _, ok := keep[strings.TrimSpace(idValue)]; !ok {
			toDelete = append(toDelete, idValue)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, idValue := range toDelete {
		if _, err := scope.db.Exec(ctx, fmt.Sprintf(
			`DELETE FROM %s WHERE bridge_id=$1 AND login_id=$2 AND %s=$3`,
			entityTable, idColumn,
		), scope.bridgeID, scope.loginID, idValue); err != nil {
			return err
		}
		if _, err := scope.db.Exec(ctx, fmt.Sprintf(
			`DELETE FROM %s WHERE bridge_id=$1 AND login_id=$2 AND %s=$3`,
			runKeyTable, idColumn,
		), scope.bridgeID, scope.loginID, idValue); err != nil {
			return err
		}
	}
	return nil
}
