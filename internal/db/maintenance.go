package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func (m *Manager) WALSizeBytes() int64 {
	fi, err := os.Stat(m.path + "-wal")
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (m *Manager) DBSizeBytes() int64 {
	fi, err := os.Stat(m.path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (m *Manager) CheckpointIfWALExceeds(ctx context.Context, thresholdBytes int64) (bool, error) {
	if m.WALSizeBytes() <= thresholdBytes {
		return false, nil
	}
	if _, err := m.writer.ExecContext(ctx, "PRAGMA wal_checkpoint(RESTART)"); err != nil {
		return false, fmt.Errorf("wal restart checkpoint: %w", err)
	}
	return true, nil
}

func (m *Manager) CleanupOldSynced(ctx context.Context, retentionDays int, diskThresholdPct float64, dbThresholdBytes int64) (deleted int64, didRun bool, err error) {
	usagePct := diskUsagePercent(filepath.Dir(m.path))
	dbSize := m.DBSizeBytes()
	if usagePct < diskThresholdPct && dbSize < dbThresholdBytes {
		return 0, false, nil
	}

	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).UnixMilli()
	tables := []string{"llm_traces", "error_events", "system_metrics"}
	for _, table := range tables {
		res, execErr := m.writer.ExecContext(ctx, "DELETE FROM "+table+" WHERE synced = 1 AND created_at < ?", cutoff)
		if execErr != nil {
			return deleted, true, execErr
		}
		affected, _ := res.RowsAffected()
		deleted += affected
	}

	_, _ = m.writer.ExecContext(ctx, "PRAGMA incremental_vacuum(1000)")
	return deleted, true, nil
}

func diskUsagePercent(path string) float64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	total := float64(stat.Blocks) * float64(stat.Bsize)
	free := float64(stat.Bavail) * float64(stat.Bsize)
	if total <= 0 {
		return 0
	}
	used := total - free
	return (used / total) * 100
}
