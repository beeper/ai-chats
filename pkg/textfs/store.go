package textfs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
)

type Store struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
	agentID  string
}

type FileEntry struct {
	Path      string
	Content   string
	Hash      string
	Source    string
	UpdatedAt int64
}

func NewStore(db *dbutil.Database, bridgeID, loginID, agentID string) *Store {
	return &Store{
		db:       db,
		bridgeID: bridgeID,
		loginID:  loginID,
		agentID:  agentID,
	}
}

func (s *Store) Read(ctx context.Context, relPath string) (*FileEntry, bool, error) {
	path, err := NormalizePath(relPath)
	if err != nil {
		return nil, false, err
	}
	var entry FileEntry
	row := s.db.QueryRow(ctx,
		`SELECT path, content, hash, source, updated_at
         FROM ai_memory_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4`,
		s.bridgeID, s.loginID, s.agentID, path,
	)
	if err := row.Scan(&entry.Path, &entry.Content, &entry.Hash, &entry.Source, &entry.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &entry, true, nil
}

func (s *Store) Write(ctx context.Context, relPath, content string) (*FileEntry, error) {
	path, err := NormalizePath(relPath)
	if err != nil {
		return nil, err
	}
	hash := hashContent(content)
	updatedAt := time.Now().UnixMilli()
	source := ClassifySource(path)
	_, err = s.db.Exec(ctx,
		`INSERT INTO ai_memory_files
           (bridge_id, login_id, agent_id, path, source, content, hash, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
         ON CONFLICT (bridge_id, login_id, agent_id, path)
         DO UPDATE SET source=excluded.source, content=excluded.content, hash=excluded.hash, updated_at=excluded.updated_at`,
		s.bridgeID, s.loginID, s.agentID, path, source, content, hash, updatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &FileEntry{
		Path:      path,
		Content:   content,
		Hash:      hash,
		Source:    source,
		UpdatedAt: updatedAt,
	}, nil
}

// WriteIfMissing writes a file only if it does not already exist.
// Returns true if a new entry was created.
func (s *Store) WriteIfMissing(ctx context.Context, relPath, content string) (bool, error) {
	path, err := NormalizePath(relPath)
	if err != nil {
		return false, err
	}
	hash := hashContent(content)
	updatedAt := time.Now().UnixMilli()
	source := ClassifySource(path)
	result, err := s.db.Exec(ctx,
		`INSERT INTO ai_memory_files
           (bridge_id, login_id, agent_id, path, source, content, hash, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
         ON CONFLICT (bridge_id, login_id, agent_id, path)
         DO NOTHING`,
		s.bridgeID, s.loginID, s.agentID, path, source, content, hash, updatedAt,
	)
	if err != nil {
		return false, err
	}
	if result == nil {
		return false, nil
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, nil
	}
	return rows > 0, nil
}

func (s *Store) Delete(ctx context.Context, relPath string) error {
	path, err := NormalizePath(relPath)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`DELETE FROM ai_memory_files WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND path=$4`,
		s.bridgeID, s.loginID, s.agentID, path,
	)
	return err
}

func (s *Store) List(ctx context.Context) ([]FileEntry, error) {
	rows, err := s.db.Query(ctx,
		`SELECT path, content, hash, source, updated_at
         FROM ai_memory_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`,
		s.bridgeID, s.loginID, s.agentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []FileEntry
	for rows.Next() {
		var entry FileEntry
		if err := rows.Scan(&entry.Path, &entry.Content, &entry.Hash, &entry.Source, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) ListWithPrefix(ctx context.Context, dir string) ([]FileEntry, error) {
	normalizedDir, err := NormalizeDir(dir)
	if err != nil {
		return nil, err
	}
	if normalizedDir == "" {
		return s.List(ctx)
	}
	prefix := strings.TrimSuffix(normalizedDir, "/")
	rows, err := s.db.Query(ctx,
		`SELECT path, content, hash, source, updated_at
         FROM ai_memory_files
         WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND (path=$4 OR path LIKE $5)`,
		s.bridgeID, s.loginID, s.agentID, prefix, prefix+"/%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []FileEntry
	for rows.Next() {
		var entry FileEntry
		if err := rows.Scan(&entry.Path, &entry.Content, &entry.Hash, &entry.Source, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) DirEntries(entries []FileEntry, dir string) ([]string, bool) {
	prefix := ""
	normalizedDir := dir
	if normalized, err := NormalizeDir(dir); err == nil {
		normalizedDir = normalized
	}
	if normalizedDir != "" {
		prefix = normalizedDir + "/"
	}
	seen := make(map[string]bool)
	hasDir := false
	for _, entry := range entries {
		if prefix != "" && !strings.HasPrefix(entry.Path, prefix) && entry.Path != normalizedDir {
			continue
		}
		if entry.Path == normalizedDir {
			continue
		}
		hasDir = true
		rest := strings.TrimPrefix(entry.Path, prefix)
		parts := strings.SplitN(rest, "/", 2)
		name := parts[0]
		if name == "" {
			continue
		}
		if len(parts) > 1 {
			name = name + "/"
		}
		seen[name] = true
	}
	if normalizedDir == "" {
		for _, virtual := range VirtualRootEntries() {
			if virtual == "" {
				continue
			}
			if _, ok := seen[virtual]; !ok {
				seen[virtual] = true
			}
		}
	}
	if !hasDir && IsVirtualDir(normalizedDir) {
		hasDir = true
	}
	if len(seen) == 0 {
		return nil, hasDir
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names, hasDir
}

func hashContent(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}
