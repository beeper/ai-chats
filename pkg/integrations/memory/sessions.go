package memory

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"

	"maunium.net/go/mautrix/bridgev2/networkid"

	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
	memorycore "github.com/beeper/agentremote/pkg/memory"
)

type sessionState struct {
	contentHash string
}

type sessionPortal struct {
	key       string
	portalKey networkid.PortalKey
}

func (m *MemorySearchManager) activeSessionPortals(ctx context.Context) (map[string]sessionPortal, error) {
	if m == nil || m.host == nil {
		return nil, errors.New("memory search unavailable")
	}
	infos, err := m.host.SessionPortals(ctx, m.agentID)
	if err != nil {
		return nil, err
	}
	active := make(map[string]sessionPortal, len(infos))
	for _, info := range infos {
		key := strings.TrimSpace(info.Key)
		if key == "" {
			continue
		}
		active[key] = sessionPortal{key: key, portalKey: info.PortalKey}
	}
	return active, nil
}

func (m *MemorySearchManager) syncSessions(ctx context.Context, force bool, sessionKey, generation string) error {
	if m == nil || m.host == nil {
		return errors.New("memory search unavailable")
	}
	active, err := m.activeSessionPortals(ctx)
	if err != nil {
		return err
	}
	changedFiles := 0

	for key, session := range active {
		state, _ := m.loadSessionState(ctx, key)
		content, err := m.buildSessionContent(ctx, session.portalKey)
		if err != nil {
			m.log.Warn().Str("session", key).Msg("memory session delta failed: " + err.Error())
			continue
		}
		hash := memorycore.HashText(content)
		if !force && hash == state.contentHash {
			continue
		}
		changedFiles++
		if content == "" {
			if err := m.deleteSessionFile(ctx, key); err != nil {
				m.log.Warn().Err(err).Str("session", key).Msg("memory session delete failed")
			}
		} else {
			path := sessionPathForKey(key)
			if err := m.upsertSessionFile(ctx, key, path, content, hash); err != nil {
				m.log.Warn().Err(err).Str("session", key).Msg("memory session write failed")
			} else if err := m.indexContent(ctx, path, "sessions", content, generation); err != nil {
				m.log.Warn().Err(err).Str("session", key).Msg("memory session index failed")
			}
		}
		state.contentHash = hash
		_ = m.saveSessionState(ctx, key, state)
	}

	m.log.Debug().
		Int("files", len(active)).
		Bool("needsFullReindex", force).
		Int("dirtyFiles", changedFiles).
		Int("concurrency", 1).
		Msg("memory sync: indexing session files")

	if err := m.removeStaleSessions(ctx, active); err != nil {
		return err
	}
	m.pruneExpiredSessions(ctx)
	return nil
}

func (m *MemorySearchManager) loadSessionState(ctx context.Context, sessionKey string) (sessionState, error) {
	var state sessionState
	row := m.db.QueryRow(ctx,
		`SELECT content_hash
         FROM aichats_memory_session_state
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.baseArgs(sessionKey)...,
	)
	switch err := row.Scan(&state.contentHash); err {
	case nil:
		return state, nil
	case sql.ErrNoRows:
		return sessionState{}, nil
	default:
		return sessionState{}, err
	}
}

func (m *MemorySearchManager) saveSessionState(ctx context.Context, sessionKey string, state sessionState) error {
	_, err := m.db.Exec(ctx,
		`INSERT INTO aichats_memory_session_state
           (bridge_id, login_id, agent_id, session_key, content_hash, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6)
         ON CONFLICT (bridge_id, login_id, agent_id, session_key)
         DO UPDATE SET content_hash=excluded.content_hash, updated_at=excluded.updated_at`,
		m.baseArgs(sessionKey, state.contentHash, time.Now().UnixMilli())...,
	)
	return err
}

func (m *MemorySearchManager) buildSessionContent(ctx context.Context, portalKey networkid.PortalKey) (string, error) {
	transcript, err := m.host.SessionTranscript(ctx, portalKey)
	if err != nil {
		return "", err
	}
	if len(transcript) == 0 {
		return "", nil
	}

	var lines []string
	for _, msg := range transcript {
		if !shouldIncludeSessionMessage(msg, m.agentID) {
			continue
		}
		text := normalizeSessionText(msg.Body)
		if text == "" {
			continue
		}
		label := "User"
		if strings.ToLower(strings.TrimSpace(msg.Role)) == "assistant" {
			label = "Assistant"
		}
		lines = append(lines, label+": "+text)
	}
	if len(lines) == 0 {
		return "", nil
	}
	return strings.Join(lines, "\n"), nil
}

func (m *MemorySearchManager) upsertSessionFile(ctx context.Context, sessionKey, path, content, hash string) error {
	var existingPath string
	row := m.db.QueryRow(ctx,
		`SELECT path FROM aichats_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.baseArgs(sessionKey)...,
	)
	switch err := row.Scan(&existingPath); err {
	case nil:
		if existingPath != "" && existingPath != path {
			m.purgeSessionPath(ctx, existingPath)
		}
	case sql.ErrNoRows:
	default:
		return err
	}
	_, err := m.db.Exec(ctx,
		`INSERT INTO aichats_memory_session_files
           (bridge_id, login_id, agent_id, session_key, path, content, hash, size, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
         ON CONFLICT (bridge_id, login_id, agent_id, session_key)
         DO UPDATE SET path=excluded.path, content=excluded.content, hash=excluded.hash,
           size=excluded.size, updated_at=excluded.updated_at`,
		m.baseArgs(sessionKey, path, content, hash, len(content), time.Now().UnixMilli())...,
	)
	return err
}

func (m *MemorySearchManager) deleteSessionFile(ctx context.Context, sessionKey string) error {
	var path string
	row := m.db.QueryRow(ctx,
		`SELECT path FROM aichats_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.baseArgs(sessionKey)...,
	)
	if err := row.Scan(&path); err != nil && err != sql.ErrNoRows {
		return err
	}
	m.purgeSessionPath(ctx, path)
	_, _ = m.db.Exec(ctx,
		`DELETE FROM aichats_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.baseArgs(sessionKey)...,
	)
	return nil
}

func (m *MemorySearchManager) removeStaleSessions(ctx context.Context, active map[string]sessionPortal) error {
	rows, err := m.db.Query(ctx,
		`SELECT session_key, path FROM aichats_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
		m.baseArgs()...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var sessionKey, path string
		if err := rows.Scan(&sessionKey, &path); err != nil {
			return err
		}
		if _, ok := active[sessionKey]; ok {
			continue
		}
		m.purgeSessionData(ctx, sessionKey, path)
	}
	return rows.Err()
}

func shouldIncludeSessionMessage(msg integrationruntime.MessageSummary, agentID string) bool {
	if strings.TrimSpace(msg.Body) == "" || msg.ExcludeFromHistory {
		return false
	}
	role := strings.ToLower(strings.TrimSpace(msg.Role))
	if role != "user" && role != "assistant" {
		return false
	}
	if role == "assistant" && strings.TrimSpace(msg.AgentID) != "" && strings.TrimSpace(msg.AgentID) != strings.TrimSpace(agentID) {
		return false
	}
	return true
}

func normalizeSessionText(text string) string {
	text = normalizeNewlines(text)
	var b strings.Builder
	prevSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

func sessionPathForKey(sessionKey string) string {
	cleaned := strings.TrimSpace(sessionKey)
	if cleaned == "" {
		cleaned = "main"
	}
	cleaned = strings.ReplaceAll(cleaned, "/", "_")
	cleaned = strings.ReplaceAll(cleaned, "\\", "_")
	return "sessions/" + cleaned + ".jsonl"
}
