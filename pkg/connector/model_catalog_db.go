package connector

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"go.mau.fi/util/dbutil"
)

type modelCatalogDBScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

func (oc *AIClient) modelCatalogDBScope() *modelCatalogDBScope {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return nil
	}
	db := oc.bridgeDB()
	if db == nil {
		return nil
	}
	return &modelCatalogDBScope{
		db:       db,
		bridgeID: string(oc.UserLogin.Bridge.DB.BridgeID),
		loginID:  string(oc.UserLogin.ID),
	}
}

func encodeModelCatalogInput(input []string) (string, error) {
	if len(input) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeModelCatalogInput(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var input []string
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil, err
	}
	return input, nil
}

func modelCatalogEntriesEqual(left []ModelCatalogEntry, right []ModelCatalogEntry) bool {
	return slices.EqualFunc(left, right, func(a, b ModelCatalogEntry) bool {
		return a.ID == b.ID &&
			a.Name == b.Name &&
			a.Provider == b.Provider &&
			a.ContextWindow == b.ContextWindow &&
			a.MaxOutputTokens == b.MaxOutputTokens &&
			a.Reasoning == b.Reasoning &&
			slices.Equal(a.Input, b.Input)
	})
}

func (oc *AIClient) loadModelCatalogRows(ctx context.Context) ([]ModelCatalogEntry, error) {
	scope := oc.modelCatalogDBScope()
	if scope == nil {
		return nil, nil
	}
	rows, err := scope.db.Query(ctx, `
		SELECT provider, model_id, name, context_window, max_output_tokens, reasoning, input_json
		FROM ai_model_catalog_entries
		WHERE bridge_id=$1 AND login_id=$2
		ORDER BY provider, model_id
	`, scope.bridgeID, scope.loginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []ModelCatalogEntry
	for rows.Next() {
		var (
			entry     ModelCatalogEntry
			inputJSON string
		)
		if err := rows.Scan(
			&entry.Provider,
			&entry.ID,
			&entry.Name,
			&entry.ContextWindow,
			&entry.MaxOutputTokens,
			&entry.Reasoning,
			&inputJSON,
		); err != nil {
			return nil, err
		}
		input, err := decodeModelCatalogInput(inputJSON)
		if err != nil {
			return nil, err
		}
		entry.Input = input
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (oc *AIClient) replaceModelCatalogRows(ctx context.Context, entries []ModelCatalogEntry) error {
	scope := oc.modelCatalogDBScope()
	if scope == nil {
		return nil
	}
	return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		if _, err := scope.db.Exec(ctx,
			`DELETE FROM ai_model_catalog_entries WHERE bridge_id=$1 AND login_id=$2`,
			scope.bridgeID, scope.loginID,
		); err != nil {
			return err
		}
		for _, entry := range entries {
			inputJSON, err := encodeModelCatalogInput(entry.Input)
			if err != nil {
				return err
			}
			if _, err := scope.db.Exec(ctx, `
				INSERT INTO ai_model_catalog_entries (
					bridge_id, login_id, provider, model_id, name,
					context_window, max_output_tokens, reasoning, input_json
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			`,
				scope.bridgeID,
				scope.loginID,
				entry.Provider,
				entry.ID,
				entry.Name,
				entry.ContextWindow,
				entry.MaxOutputTokens,
				entry.Reasoning,
				inputJSON,
			); err != nil {
				return err
			}
		}
		return nil
	})
}
