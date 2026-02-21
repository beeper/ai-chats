package connector

import (
	"context"

	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
)

type cronSessionEntry = integrationcron.SessionEntry

type cronSessionStore = integrationcron.SessionStore

const cronSessionStorePath = integrationcron.SessionStorePath

func cronSessionKey(agentID, jobID string) string {
	return integrationcron.CronSessionKey(agentID, jobID, normalizeAgentID)
}

func (oc *AIClient) loadCronSessionStore(ctx context.Context) (cronSessionStore, error) {
	return integrationcron.LoadSessionStore(ctx, oc.bridgeStateBackend(), newCronLogger(oc.log))
}

func (oc *AIClient) saveCronSessionStore(ctx context.Context, store cronSessionStore) error {
	return integrationcron.SaveSessionStore(ctx, oc.bridgeStateBackend(), store)
}

func (oc *AIClient) updateCronSessionEntry(ctx context.Context, sessionKey string, updater func(entry cronSessionEntry) cronSessionEntry) {
	if oc == nil {
		return
	}
	integrationcron.UpdateSessionEntry(
		ctx,
		oc.bridgeStateBackend(),
		newCronLogger(oc.log),
		sessionKey,
		func(entry integrationcron.SessionEntry) integrationcron.SessionEntry {
			if updater == nil {
				return entry
			}
			return updater(entry)
		},
	)
}
