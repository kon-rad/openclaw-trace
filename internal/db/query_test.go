package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLatestRowQueries(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()

	err = dbm.InsertBatch(context.Background(),
		[]TraceInsert{{
			TraceID:   "11111111-1111-4111-8111-111111111111",
			CreatedAt: 1,
			Provider:  "anthropic",
			Model:     "claude-sonnet-4",
			Status:    "ok",
		}},
		[]ErrorInsert{{
			TraceID:   "22222222-2222-4222-8222-222222222222",
			CreatedAt: 2,
			ErrorType: "llm_error",
			Message:   "rate limited",
			Severity:  "error",
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("insert batch: %v", err)
	}

	traceRow, err := dbm.LatestTrace(context.Background())
	if err != nil {
		t.Fatalf("latest trace: %v", err)
	}
	if traceRow.Provider != "anthropic" || traceRow.Model != "claude-sonnet-4" {
		t.Fatalf("unexpected trace row: %+v", traceRow)
	}

	errorRow, err := dbm.LatestError(context.Background())
	if err != nil {
		t.Fatalf("latest error: %v", err)
	}
	if errorRow.ErrorType != "llm_error" || errorRow.Message != "rate limited" {
		t.Fatalf("unexpected error row: %+v", errorRow)
	}
}
