package connector

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

type sessionState struct {
	lastRowID       int64
	pendingBytes    int
	pendingMessages int
}

type sessionPortal struct {
	key       string
	portalKey networkid.PortalKey
}

func (m *MemorySearchManager) syncSessions(ctx context.Context, force bool, sessionKey, generation string) error {
	if m == nil || m.client == nil {
		return fmt.Errorf("memory search unavailable")
	}
	// For shared portals (portal.Receiver == ""), only index sessions for portals that this login is
	// actually in. Otherwise a login could accidentally index unrelated shared portals.
	allowedShared := map[string]struct{}{}
	if m.client.UserLogin != nil && m.client.UserLogin.Bridge != nil && m.client.UserLogin.Bridge.DB != nil {
		if ups, err := m.client.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, m.client.UserLogin.UserLogin); err == nil {
			for _, up := range ups {
				if up == nil {
					continue
				}
				if up.Portal.Receiver != "" {
					// Private portal keys are already scoped by receiver.
					continue
				}
				allowedShared[up.Portal.String()] = struct{}{}
			}
		}
	}

	portals, err := m.client.UserLogin.Bridge.DB.Portal.GetAll(ctx)
	if err != nil {
		return err
	}

	active := make(map[string]sessionPortal)
	for _, portal := range portals {
		if portal == nil {
			continue
		}
		if portal.MXID == "" {
			continue
		}
		if portal.Receiver != "" && string(portal.Receiver) != m.loginID {
			continue
		}
		if portal.Receiver == "" && len(allowedShared) > 0 {
			if _, ok := allowedShared[portal.PortalKey.String()]; !ok {
				continue
			}
		}
		meta, ok := portal.Metadata.(*PortalMetadata)
		if !ok || meta == nil {
			continue
		}
		if meta.IsCronRoom {
			continue
		}
		if resolveAgentID(meta) != m.agentID {
			continue
		}
		key := portal.PortalKey.String()
		if key == "" {
			continue
		}
		active[key] = sessionPortal{key: key, portalKey: portal.PortalKey}
	}

	indexAll := force
	if !indexAll {
		var count int
		row := m.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM ai_memory_session_state WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
			m.bridgeID, m.loginID, m.agentID,
		)
		if err := row.Scan(&count); err == nil && count == 0 {
			indexAll = true
		}
	}

	dirtyFiles := 0
	row := m.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM ai_memory_session_state
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3
           AND (pending_bytes > 0 OR pending_messages > 0)`,
		m.bridgeID, m.loginID, m.agentID,
	)
	_ = row.Scan(&dirtyFiles)

	m.log.Debug().
		Int("files", len(active)).
		Bool("needsFullReindex", force).
		Int("dirtyFiles", dirtyFiles).
		Bool("batch", m.batchEnabled).
		Int("concurrency", m.indexConcurrency()).
		Msg("memory sync: indexing session files")

	for key, session := range active {
		state, _ := m.loadSessionState(ctx, key)
		maxRowID, deltaBytes, deltaMessages, err := m.computeSessionDelta(ctx, session.portalKey, state.lastRowID)
		if err != nil {
			m.log.Warn().Str("session", key).Msg("memory session delta failed: " + err.Error())
			continue
		}

		needsFullReindex := false
		if maxRowID < state.lastRowID {
			needsFullReindex = true
			state.lastRowID = 0
			state.pendingBytes = 0
			state.pendingMessages = 0
		}

		state.lastRowID = maxRowID
		state.pendingBytes += deltaBytes
		state.pendingMessages += deltaMessages

		shouldIndex := indexAll || needsFullReindex
		if !shouldIndex && sessionKey != "" && sessionKey == key && state.lastRowID == 0 {
			shouldIndex = true
		}

		if !shouldIndex {
			thresholdBytes := m.cfg.Sync.Sessions.DeltaBytes
			thresholdMessages := m.cfg.Sync.Sessions.DeltaMessages
			bytesHit := thresholdBytes <= 0 && state.pendingBytes > 0
			if thresholdBytes > 0 && state.pendingBytes >= thresholdBytes {
				bytesHit = true
			}
			messagesHit := thresholdMessages <= 0 && state.pendingMessages > 0
			if thresholdMessages > 0 && state.pendingMessages >= thresholdMessages {
				messagesHit = true
			}
			if bytesHit || messagesHit {
				shouldIndex = true
			}
		}

		if shouldIndex {
			content, latestRowID, err := m.buildSessionContent(ctx, session.portalKey)
			if err != nil {
				m.log.Warn().Err(err).Str("session", key).Msg("memory session read failed")
			} else if content == "" {
				_ = m.deleteSessionFile(ctx, key)
			} else {
				path := sessionPathForKey(key)
				hash := hashSessionContent(content)
				existingHash, _ := m.getSessionFileHash(ctx, key)
				if needsFullReindex || indexAll || existingHash == "" || existingHash != hash {
					if err := m.upsertSessionFile(ctx, key, path, content, hash); err != nil {
						m.log.Warn().Err(err).Str("session", key).Msg("memory session write failed")
					} else if err := m.indexContent(ctx, path, "sessions", content, generation); err != nil {
						m.log.Warn().Err(err).Str("session", key).Msg("memory session index failed")
					}
				}
				if latestRowID > 0 {
					state.lastRowID = latestRowID
				}
				state.pendingBytes = 0
				state.pendingMessages = 0
			}
		}

		_ = m.saveSessionState(ctx, key, state)
	}

	if err := m.removeStaleSessions(ctx, active); err != nil {
		return err
	}
	return nil
}

func (m *MemorySearchManager) loadSessionState(ctx context.Context, sessionKey string) (sessionState, error) {
	var state sessionState
	row := m.db.QueryRow(ctx,
		`SELECT last_rowid, pending_bytes, pending_messages
         FROM ai_memory_session_state
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.bridgeID, m.loginID, m.agentID, sessionKey,
	)
	switch err := row.Scan(&state.lastRowID, &state.pendingBytes, &state.pendingMessages); err {
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
		`INSERT INTO ai_memory_session_state
           (bridge_id, login_id, agent_id, session_key, last_rowid, pending_bytes, pending_messages, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
         ON CONFLICT (bridge_id, login_id, agent_id, session_key)
         DO UPDATE SET last_rowid=excluded.last_rowid, pending_bytes=excluded.pending_bytes,
           pending_messages=excluded.pending_messages, updated_at=excluded.updated_at`,
		m.bridgeID, m.loginID, m.agentID, sessionKey,
		state.lastRowID, state.pendingBytes, state.pendingMessages, time.Now().UnixMilli(),
	)
	return err
}

func (m *MemorySearchManager) computeSessionDelta(ctx context.Context, portalKey networkid.PortalKey, lastRowID int64) (int64, int, int, error) {
	var maxRowID sql.NullInt64
	row := m.db.QueryRow(ctx,
		`SELECT MAX(rowid) FROM message WHERE bridge_id=$1 AND room_id=$2 AND room_receiver=$3`,
		m.bridgeID, portalKey.ID, portalKey.Receiver,
	)
	if err := row.Scan(&maxRowID); err != nil {
		return lastRowID, 0, 0, err
	}
	if !maxRowID.Valid {
		return 0, 0, 0, nil
	}
	if maxRowID.Int64 <= lastRowID {
		return maxRowID.Int64, 0, 0, nil
	}

	rows, err := m.db.Query(ctx,
		`SELECT rowid, metadata FROM message
         WHERE bridge_id=$1 AND room_id=$2 AND room_receiver=$3 AND rowid > $4
         ORDER BY rowid ASC`,
		m.bridgeID, portalKey.ID, portalKey.Receiver, lastRowID,
	)
	if err != nil {
		return maxRowID.Int64, 0, 0, err
	}
	defer rows.Close()

	deltaBytes := 0
	deltaMessages := 0
	for rows.Next() {
		var rowid int64
		var rawMeta []byte
		if err := rows.Scan(&rowid, &rawMeta); err != nil {
			return maxRowID.Int64, 0, 0, err
		}
		if rowid > maxRowID.Int64 {
			maxRowID.Int64 = rowid
		}
		meta := parseSessionMetadata(rawMeta)
		if meta == nil || !shouldIncludeInHistory(meta) {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(meta.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		if role == "assistant" && meta.AgentID != "" && meta.AgentID != m.agentID {
			continue
		}
		text := normalizeSessionText(meta.Body)
		if text == "" {
			continue
		}
		label := "User"
		if role == "assistant" {
			label = "Assistant"
		}
		line := label + ": " + text
		deltaMessages++
		deltaBytes += len([]byte(line)) + 1
	}
	if err := rows.Err(); err != nil {
		return maxRowID.Int64, 0, 0, err
	}

	return maxRowID.Int64, deltaBytes, deltaMessages, nil
}

func (m *MemorySearchManager) buildSessionContent(ctx context.Context, portalKey networkid.PortalKey) (string, int64, error) {
	rows, err := m.db.Query(ctx,
		`SELECT rowid, metadata FROM message
         WHERE bridge_id=$1 AND room_id=$2 AND room_receiver=$3
         ORDER BY rowid ASC`,
		m.bridgeID, portalKey.ID, portalKey.Receiver,
	)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()

	var lines []string
	var maxRowID int64
	for rows.Next() {
		var rowid int64
		var rawMeta []byte
		if err := rows.Scan(&rowid, &rawMeta); err != nil {
			return "", 0, err
		}
		if rowid > maxRowID {
			maxRowID = rowid
		}
		meta := parseSessionMetadata(rawMeta)
		if meta == nil || !shouldIncludeInHistory(meta) {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(meta.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		if role == "assistant" && meta.AgentID != "" && meta.AgentID != m.agentID {
			continue
		}
		text := normalizeSessionText(meta.Body)
		if text == "" {
			continue
		}
		label := "User"
		if role == "assistant" {
			label = "Assistant"
		}
		lines = append(lines, label+": "+text)
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	if len(lines) == 0 {
		return "", maxRowID, nil
	}
	return strings.Join(lines, "\n"), maxRowID, nil
}

func (m *MemorySearchManager) getSessionFileHash(ctx context.Context, sessionKey string) (string, error) {
	var hash string
	row := m.db.QueryRow(ctx,
		`SELECT hash FROM ai_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.bridgeID, m.loginID, m.agentID, sessionKey,
	)
	switch err := row.Scan(&hash); err {
	case nil:
		return hash, nil
	case sql.ErrNoRows:
		return "", nil
	default:
		return "", err
	}
}

func (m *MemorySearchManager) upsertSessionFile(ctx context.Context, sessionKey, path, content, hash string) error {
	var existingPath string
	row := m.db.QueryRow(ctx,
		`SELECT path FROM ai_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.bridgeID, m.loginID, m.agentID, sessionKey,
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
	size := len([]byte(content))
	_, err := m.db.Exec(ctx,
		`INSERT INTO ai_memory_session_files
           (bridge_id, login_id, agent_id, session_key, path, content, hash, size, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
         ON CONFLICT (bridge_id, login_id, agent_id, session_key)
         DO UPDATE SET path=excluded.path, content=excluded.content, hash=excluded.hash,
           size=excluded.size, updated_at=excluded.updated_at`,
		m.bridgeID, m.loginID, m.agentID, sessionKey, path, content, hash, size, time.Now().UnixMilli(),
	)
	return err
}

func (m *MemorySearchManager) deleteSessionFile(ctx context.Context, sessionKey string) error {
	var path string
	row := m.db.QueryRow(ctx,
		`SELECT path FROM ai_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.bridgeID, m.loginID, m.agentID, sessionKey,
	)
	if err := row.Scan(&path); err != nil && err != sql.ErrNoRows {
		return err
	}
	m.purgeSessionPath(ctx, path)
	_, _ = m.db.Exec(ctx,
		`DELETE FROM ai_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
		m.bridgeID, m.loginID, m.agentID, sessionKey,
	)
	return nil
}

func (m *MemorySearchManager) removeStaleSessions(ctx context.Context, active map[string]sessionPortal) error {
	rows, err := m.db.Query(ctx,
		`SELECT session_key, path FROM ai_memory_session_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
		m.bridgeID, m.loginID, m.agentID,
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
		m.purgeSessionPath(ctx, path)
		_, _ = m.db.Exec(ctx,
			`DELETE FROM ai_memory_session_files
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
			m.bridgeID, m.loginID, m.agentID, sessionKey,
		)
		_, _ = m.db.Exec(ctx,
			`DELETE FROM ai_memory_session_state
             WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND session_key=$4`,
			m.bridgeID, m.loginID, m.agentID, sessionKey,
		)
	}
	return rows.Err()
}

func parseSessionMetadata(raw []byte) *MessageMetadata {
	if len(raw) == 0 {
		return nil
	}
	var meta MessageMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil
	}
	return &meta
}

func normalizeSessionText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var b strings.Builder
	prevSpace := false
	for _, r := range text {
		if r == '\n' || r == '\t' || r == ' ' || r == '\v' || r == '\f' || r == '\u00a0' || r == '\u2000' || r == '\u2001' || r == '\u2002' || r == '\u2003' || r == '\u2004' || r == '\u2005' || r == '\u2006' || r == '\u2007' || r == '\u2008' || r == '\u2009' || r == '\u200a' || r == '\u202f' || r == '\u205f' || r == '\u3000' {
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

func hashSessionContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
