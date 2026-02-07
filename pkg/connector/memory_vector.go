package connector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

const memoryVectorTable = "ai_memory_chunks_vec"

// loadExtensionEnabler matches github.com/mattn/go-sqlite3's (*SQLiteConn).EnableLoadExtension.
// Declared as an interface to avoid importing the sqlite3 package (and forcing CGO in builds
// that might not need the SQLite driver).
type loadExtensionEnabler interface {
	EnableLoadExtension(enable bool) error
}

func (m *MemorySearchManager) ensureVectorConn(ctx context.Context) {
	if m == nil || m.cfg == nil || !m.cfg.Store.Vector.Enabled {
		return
	}
	m.mu.Lock()
	if m.vectorConn != nil || m.vectorError != "" {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	conn, err := m.db.RawDB.Conn(ctx)
	if err != nil {
		m.mu.Lock()
		m.vectorError = err.Error()
		m.mu.Unlock()
		return
	}

	if path := m.cfg.Store.Vector.ExtensionPath; path != "" {
		// Best-effort: enable extension loading for SQLite connections that support it.
		// If the driver doesn't support enabling extensions, the subsequent load may fail and
		// we surface that error normally.
		_ = conn.Raw(func(driverConn any) error {
			if enabler, ok := driverConn.(loadExtensionEnabler); ok {
				return enabler.EnableLoadExtension(true)
			}
			return nil
		})
		if _, err := conn.ExecContext(ctx, "SELECT load_extension(?)", path); err != nil {
			_ = conn.Close()
			m.mu.Lock()
			m.vectorError = err.Error()
			m.mu.Unlock()
			return
		}
		_ = conn.Raw(func(driverConn any) error {
			if enabler, ok := driverConn.(loadExtensionEnabler); ok {
				return enabler.EnableLoadExtension(false)
			}
			return nil
		})
	}

	m.mu.Lock()
	// Another goroutine may have initialized the connection while we were opening/loading.
	// Prefer the first connection and close this one to avoid leaking *sql.Conn.
	if m.vectorConn != nil || m.vectorError != "" {
		m.mu.Unlock()
		_ = conn.Close()
		return
	}
	m.vectorConn = conn
	m.vectorReady = true
	m.mu.Unlock()
}

func (m *MemorySearchManager) ensureVectorTable(ctx context.Context, dims int) bool {
	if m == nil || dims <= 0 {
		return false
	}
	m.ensureVectorConn(ctx)
	m.mu.Lock()
	conn := m.vectorConn
	ready := m.vectorReady
	m.mu.Unlock()
	if conn == nil || !ready {
		return false
	}
	_, err := conn.ExecContext(ctx, fmt.Sprintf(
		"CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(id TEXT PRIMARY KEY, embedding FLOAT[%d]);",
		memoryVectorTable, dims,
	))
	if err != nil {
		m.mu.Lock()
		m.vectorReady = false
		m.vectorError = err.Error()
		m.mu.Unlock()
		return false
	}
	return true
}

func vectorToBlob(values []float64) []byte {
	if len(values) == 0 {
		return nil
	}
	buf := make([]byte, 0, len(values)*4)
	for _, v := range values {
		f := float32(v)
		bits := math.Float32bits(f)
		buf = append(buf, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	}
	return buf
}

func (m *MemorySearchManager) execVector(ctx context.Context, query string, args ...any) (sql.Result, error) {
	m.mu.Lock()
	conn := m.vectorConn
	m.mu.Unlock()
	if conn == nil {
		return nil, errors.New("vector extension unavailable")
	}
	return conn.ExecContext(ctx, query, args...)
}

func (m *MemorySearchManager) queryVector(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	m.mu.Lock()
	conn := m.vectorConn
	m.mu.Unlock()
	if conn == nil {
		return nil, errors.New("vector extension unavailable")
	}
	return conn.QueryContext(ctx, query, args...)
}

func (m *MemorySearchManager) deleteVectorIDs(ctx context.Context, ids []string) {
	if m == nil || len(ids) == 0 {
		return
	}
	m.mu.Lock()
	ready := m.vectorReady
	m.mu.Unlock()
	if !ready {
		return
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		_, _ = m.execVector(ctx, fmt.Sprintf("DELETE FROM %s WHERE id=?", memoryVectorTable), id)
	}
}
