package connector

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"

	"github.com/beeper/ai-bridge/pkg/cron"
)

type bridgeDBBackend struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

func (b *bridgeDBBackend) Read(ctx context.Context, key string) ([]byte, bool, error) {
	if b == nil || b.db == nil {
		return nil, false, errors.New("bridge state store not available")
	}
	var content string
	err := b.db.QueryRow(ctx,
		`SELECT content FROM ai_bridge_state WHERE bridge_id=$1 AND login_id=$2 AND store_key=$3`,
		b.bridgeID, b.loginID, key,
	).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return []byte(content), true, nil
}

func (b *bridgeDBBackend) Write(ctx context.Context, key string, data []byte) error {
	if b == nil || b.db == nil {
		return errors.New("bridge state store not available")
	}
	_, err := b.db.Exec(ctx,
		`INSERT INTO ai_bridge_state (bridge_id, login_id, store_key, content, updated_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (bridge_id, login_id, store_key)
		 DO UPDATE SET content=excluded.content, updated_at=excluded.updated_at`,
		b.bridgeID, b.loginID, key, string(data), time.Now().UnixMilli(),
	)
	return err
}

func (b *bridgeDBBackend) List(ctx context.Context, prefix string) ([]cron.StoreEntry, error) {
	if b == nil || b.db == nil {
		return nil, errors.New("bridge state store not available")
	}
	trimmed := strings.TrimSuffix(prefix, "/")
	rows, err := b.db.Query(ctx,
		`SELECT store_key, content FROM ai_bridge_state
		 WHERE bridge_id=$1 AND login_id=$2 AND (store_key=$3 OR store_key LIKE $4)`,
		b.bridgeID, b.loginID, trimmed, trimmed+"/%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []cron.StoreEntry
	for rows.Next() {
		var key, content string
		if err := rows.Scan(&key, &content); err != nil {
			return nil, err
		}
		entries = append(entries, cron.StoreEntry{Key: key, Data: []byte(content)})
	}
	return entries, rows.Err()
}

func (oc *AIClient) bridgeStateBackend() cron.StoreBackend {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return nil
	}
	return &bridgeDBBackend{
		db:       oc.UserLogin.Bridge.DB.Database,
		bridgeID: string(oc.UserLogin.Bridge.DB.BridgeID),
		loginID:  string(oc.UserLogin.ID),
	}
}
