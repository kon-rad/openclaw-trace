package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAppliesPragmasAndSchema(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		_ = dbm.Close()
	}()

	journal, busy, autoVacuum, err := dbm.Pragmas(context.Background())
	if err != nil {
		t.Fatalf("Pragmas() error = %v", err)
	}
	if journal != "wal" {
		t.Fatalf("journal mode = %q, want wal", journal)
	}
	if busy != 10000 {
		t.Fatalf("busy_timeout = %d, want 10000", busy)
	}
	if autoVacuum != 2 {
		t.Fatalf("auto_vacuum = %d, want 2", autoVacuum)
	}

	unsynced, err := dbm.UnsyncedCount(context.Background())
	if err != nil {
		t.Fatalf("UnsyncedCount() error = %v", err)
	}
	if unsynced != 0 {
		t.Fatalf("unsynced count = %d, want 0", unsynced)
	}
}
