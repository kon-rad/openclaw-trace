package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupOldSyncedDeletesRowsWhenThresholdForced(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()

	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	newTS := time.Now().UnixMilli()

	_, err = dbm.writer.Exec(`
INSERT INTO llm_traces (trace_id, created_at, provider, model, status, synced) VALUES
('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', ?, 'anthropic', 'claude', 'ok', 1),
('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', ?, 'anthropic', 'claude', 'ok', 1)
`, old, newTS)
	if err != nil {
		t.Fatalf("insert seed traces: %v", err)
	}

	deleted, didRun, err := dbm.CleanupOldSynced(context.Background(), 1, 0, 0)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if !didRun {
		t.Fatalf("expected cleanup to run")
	}
	if deleted < 1 {
		t.Fatalf("expected at least one deleted row, got %d", deleted)
	}

	count, err := dbm.TraceCount(context.Background())
	if err != nil {
		t.Fatalf("trace count: %v", err)
	}
	if count != 1 {
		t.Fatalf("remaining trace rows = %d, want 1", count)
	}
}

func TestCheckpointIfWALExceeds(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()

	// Generate write activity so WAL file exists.
	for i := 0; i < 10; i++ {
		_, err = dbm.writer.Exec(`
INSERT INTO llm_traces (trace_id, created_at, provider, model, status, synced)
VALUES (?, ?, 'anthropic', 'claude', 'ok', 0)
`, "cccccccc-cccc-4ccc-8ccc-ccccccccccc"+string(rune('a'+i)), time.Now().UnixMilli())
		if err != nil {
			t.Fatalf("insert row: %v", err)
		}
	}

	did, err := dbm.CheckpointIfWALExceeds(context.Background(), 0)
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if !did {
		t.Fatalf("expected checkpoint to run when threshold is 0")
	}
}
