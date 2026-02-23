package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	_ "modernc.org/sqlite"
)

type Manager struct {
	path   string
	writer *sql.DB
	reader *sql.DB
}

type HealthStats struct {
	DBStatus    string
	DBSizeBytes int64
	WALSize     int64
}

const pragmaSQL = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 10000;
PRAGMA temp_store = MEMORY;
PRAGMA auto_vacuum = INCREMENTAL;
PRAGMA foreign_keys = ON;
PRAGMA cache_size = -8000;
`

func init() {
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, _ string) error {
		_, err := conn.ExecContext(context.Background(), pragmaSQL, []driver.NamedValue{})
		return err
	})
}

func Open(path string) (*Manager, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	dsn := "file:" + path
	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open writer db: %w", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)

	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("open reader db: %w", err)
	}
	reader.SetMaxOpenConns(4)
	reader.SetMaxIdleConns(4)

	if err := writer.PingContext(context.Background()); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return nil, fmt.Errorf("ping writer: %w", err)
	}
	if err := reader.PingContext(context.Background()); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return nil, fmt.Errorf("ping reader: %w", err)
	}

	if err := ensureAutoVacuum(writer); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return nil, fmt.Errorf("ensure auto_vacuum incremental: %w", err)
	}

	if _, err := writer.Exec(schemaDDL); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &Manager{
		path:   path,
		writer: writer,
		reader: reader,
	}, nil
}

func (m *Manager) Path() string {
	return m.path
}

func (m *Manager) Checkpoint(ctx context.Context) error {
	_, err := m.writer.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

func (m *Manager) Close() error {
	var errs []error
	if err := m.writer.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := m.reader.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (m *Manager) Ping(ctx context.Context) error {
	return m.writer.PingContext(ctx)
}

func (m *Manager) Stats() HealthStats {
	stats := HealthStats{
		DBStatus: "ok",
	}
	if err := m.Ping(context.Background()); err != nil {
		stats.DBStatus = "error"
	}
	if fi, err := os.Stat(m.path); err == nil {
		stats.DBSizeBytes = fi.Size()
	}
	if fi, err := os.Stat(m.path + "-wal"); err == nil {
		stats.WALSize = fi.Size()
	}
	return stats
}

func (m *Manager) Pragmas(ctx context.Context) (journalMode string, busyTimeout int, autoVacuum int, err error) {
	if err = m.writer.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return "", 0, 0, err
	}
	if err = m.writer.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		return "", 0, 0, err
	}
	if err = m.writer.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil {
		return "", 0, 0, err
	}
	return journalMode, busyTimeout, autoVacuum, nil
}

func (m *Manager) UnsyncedCount(ctx context.Context) (int64, error) {
	query := `
SELECT
  (SELECT COUNT(*) FROM llm_traces WHERE synced = 0) +
  (SELECT COUNT(*) FROM error_events WHERE synced = 0) +
  (SELECT COUNT(*) FROM system_metrics WHERE synced = 0)
`
	var count int64
	if err := m.reader.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (m *Manager) WaitForOpen(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := m.Ping(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database did not become ready within %s", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func ensureAutoVacuum(writer *sql.DB) error {
	var mode int
	if err := writer.QueryRow("PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return err
	}
	if mode == 2 {
		return nil
	}
	if _, err := writer.Exec("PRAGMA auto_vacuum = INCREMENTAL;"); err != nil {
		return err
	}
	if _, err := writer.Exec("VACUUM;"); err != nil {
		return err
	}
	return nil
}
