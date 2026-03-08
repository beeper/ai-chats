package connector

import (
	"context"
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
		SELECT provider, model_id, name, context_window, max_output_tokens, reasoning
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
		var entry ModelCatalogEntry
		if err := rows.Scan(
			&entry.Provider,
			&entry.ID,
			&entry.Name,
			&entry.ContextWindow,
			&entry.MaxOutputTokens,
			&entry.Reasoning,
		); err != nil {
			return nil, err
		}
		entry.Input, err = loadModelCatalogInputs(ctx, scope, entry.Provider, entry.ID)
		if err != nil {
			return nil, err
		}
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
		if _, err := scope.db.Exec(ctx,
			`DELETE FROM ai_model_catalog_inputs WHERE bridge_id=$1 AND login_id=$2`,
			scope.bridgeID, scope.loginID,
		); err != nil {
			return err
		}
		for _, entry := range entries {
			if _, err := scope.db.Exec(ctx, `
				INSERT INTO ai_model_catalog_entries (
					bridge_id, login_id, provider, model_id, name,
					context_window, max_output_tokens, reasoning
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			`,
				scope.bridgeID,
				scope.loginID,
				entry.Provider,
				entry.ID,
				entry.Name,
				entry.ContextWindow,
				entry.MaxOutputTokens,
				entry.Reasoning,
			); err != nil {
				return err
			}
			if err := replaceModelCatalogInputs(ctx, scope, entry.Provider, entry.ID, entry.Input); err != nil {
				return err
			}
		}
		return nil
	})
}

func loadModelCatalogInputs(ctx context.Context, scope *modelCatalogDBScope, provider, modelID string) ([]string, error) {
	rows, err := scope.db.Query(ctx, `
		SELECT input_kind
		FROM ai_model_catalog_inputs
		WHERE bridge_id=$1 AND login_id=$2 AND provider=$3 AND model_id=$4
		ORDER BY input_index
	`, scope.bridgeID, scope.loginID, provider, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var inputs []string
	for rows.Next() {
		var input string
		if err := rows.Scan(&input); err != nil {
			return nil, err
		}
		inputs = append(inputs, strings.TrimSpace(input))
	}
	return inputs, rows.Err()
}

func replaceModelCatalogInputs(ctx context.Context, scope *modelCatalogDBScope, provider, modelID string, inputs []string) error {
	for idx, input := range inputs {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if _, err := scope.db.Exec(ctx, `
			INSERT INTO ai_model_catalog_inputs (
				bridge_id, login_id, provider, model_id, input_index, input_kind
			) VALUES ($1, $2, $3, $4, $5, $6)
		`, scope.bridgeID, scope.loginID, provider, modelID, idx, input); err != nil {
			return err
		}
	}
	return nil
}
