package connector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
)

const memoryVectorTable = "ai_memory_chunks_vec"

// loadExtensionEnabler matches github.com/mattn/go-sqlite3's (*SQLiteConn).EnableLoadExtension.
// Declared as an interface to avoid importing the sqlite3 package (and forcing CGO in builds
// that might not need the SQLite driver).
type loadExtensionEnabler interface {
	EnableLoadExtension(enable bool) error
}

// vectorExtStatus caches whether the vector extension can be loaded successfully.
// This avoids re-checking the extension path on every operation while never holding
// a persistent *sql.Conn (which would deadlock with max_open_conns=1).
type vectorExtStatus struct {
	once    sync.Once
	ok      bool
	errText string
}

// withVectorConn grabs a raw *sql.Conn from the pool, loads the vector extension
// (if configured), calls fn, and always releases the connection. The extension
// validation result is cached so repeated calls skip the load_extension probe when
// the path hasn't changed.
func (m *MemorySearchManager) withVectorConn(ctx context.Context, fn func(conn *sql.Conn) error) error {
	if m == nil || m.db == nil || m.cfg == nil || !m.cfg.Store.Vector.Enabled {
		return errors.New("vector extension unavailable")
	}

	// Fast-path: if we already know the extension fails to load, don't bother grabbing a conn.
	m.mu.Lock()
	if m.vectorExtOK != nil && !m.vectorExtOK.ok {
		errText := m.vectorExtOK.errText
		m.mu.Unlock()
		return errors.New(errText)
	}
	m.mu.Unlock()

	conn, err := m.db.RawDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("vector conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := m.loadVectorExtension(ctx, conn); err != nil {
		return err
	}

	return fn(conn)
}

// loadVectorExtension loads the vector extension on conn, caching the outcome.
func (m *MemorySearchManager) loadVectorExtension(ctx context.Context, conn *sql.Conn) error {
	extPath := ""
	if m.cfg != nil {
		extPath = m.cfg.Store.Vector.ExtensionPath
	}
	if extPath == "" {
		// No extension to load — vec0 may be compiled-in.
		return nil
	}

	// Check cached result.
	m.mu.Lock()
	status := m.vectorExtOK
	m.mu.Unlock()

	if status != nil {
		if !status.ok {
			return errors.New(status.errText)
		}
		// Extension validated previously — still need to load it on this connection.
		return m.doLoadExtension(ctx, conn, extPath)
	}

	// First probe: try loading and cache the result.
	if err := m.doLoadExtension(ctx, conn, extPath); err != nil {
		s := &vectorExtStatus{ok: false, errText: err.Error()}
		m.mu.Lock()
		m.vectorExtOK = s
		m.vectorError = err.Error()
		m.mu.Unlock()
		return err
	}

	s := &vectorExtStatus{ok: true}
	m.mu.Lock()
	m.vectorExtOK = s
	m.mu.Unlock()
	return nil
}

func (m *MemorySearchManager) doLoadExtension(ctx context.Context, conn *sql.Conn, extPath string) error {
	_ = conn.Raw(func(driverConn any) error {
		if enabler, ok := driverConn.(loadExtensionEnabler); ok {
			return enabler.EnableLoadExtension(true)
		}
		return nil
	})
	if _, err := conn.ExecContext(ctx, "SELECT load_extension(?)", extPath); err != nil {
		return fmt.Errorf("vector extension load: %w", err)
	}
	_ = conn.Raw(func(driverConn any) error {
		if enabler, ok := driverConn.(loadExtensionEnabler); ok {
			return enabler.EnableLoadExtension(false)
		}
		return nil
	})
	return nil
}

func (m *MemorySearchManager) ensureVectorTable(ctx context.Context, dims int) bool {
	if m == nil || dims <= 0 {
		return false
	}
	err := m.withVectorConn(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, fmt.Sprintf(
			"CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(id TEXT PRIMARY KEY, embedding FLOAT[%d]);",
			memoryVectorTable, dims,
		))
		return err
	})
	if err != nil {
		m.mu.Lock()
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
	var result sql.Result
	err := m.withVectorConn(ctx, func(conn *sql.Conn) error {
		var execErr error
		result, execErr = conn.ExecContext(ctx, query, args...)
		return execErr
	})
	return result, err
}

func (m *MemorySearchManager) queryVectorCollect(ctx context.Context, query string, scanner func(*sql.Rows) error, args ...any) error {
	return m.withVectorConn(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		return scanner(rows)
	})
}

func (m *MemorySearchManager) deleteVectorIDs(ctx context.Context, ids []string) {
	if m == nil || len(ids) == 0 {
		return
	}
	if m.cfg == nil || !m.cfg.Store.Vector.Enabled {
		return
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		_, _ = m.execVector(ctx, fmt.Sprintf("DELETE FROM %s WHERE id=?", memoryVectorTable), id)
	}
}

// vectorAvailable returns true if the vector extension can be loaded (cached probe).
func (m *MemorySearchManager) vectorAvailable() bool {
	if m == nil || m.cfg == nil || !m.cfg.Store.Vector.Enabled {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.vectorExtOK == nil {
		return true // not yet probed — optimistic
	}
	return m.vectorExtOK.ok
}
