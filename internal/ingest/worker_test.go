package ingest

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/db"
)

func TestTryEnqueueSaturation(t *testing.T) {
	t.Parallel()

	ch := make(chan Event, 1)
	if ok := TryEnqueue(ch, Event{Kind: EventKindTrace, Trace: &TracePayload{Provider: "a", Model: "b"}}); !ok {
		t.Fatalf("expected first enqueue to succeed")
	}
	if ok := TryEnqueue(ch, Event{Kind: EventKindTrace, Trace: &TracePayload{Provider: "a", Model: "b"}}); ok {
		t.Fatalf("expected second enqueue to fail when buffer is full")
	}
}

func TestWorkerFlushesOnWindow(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()

	worker := NewWorker(slog.New(slog.NewJSONHandler(io.Discard, nil)), dbm, 1024)
	ch := make(chan Event, QueueCapacity)
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ch)
	}()

	ch <- Event{
		Kind: EventKindTrace,
		Trace: &TracePayload{
			Provider: "anthropic",
			Model:    "claude-sonnet-4",
			Status:   "ok",
		},
	}

	time.Sleep(650 * time.Millisecond)

	count, err := dbm.TraceCount(context.Background())
	if err != nil {
		t.Fatalf("trace count query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("trace count = %d, want 1", count)
	}

	close(ch)
	if err := <-done; err != nil {
		t.Fatalf("worker returned error: %v", err)
	}
}

func TestWorkerGeneratesTraceIDAndTruncatesText(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()

	worker := NewWorker(slog.New(slog.NewJSONHandler(io.Discard, nil)), dbm, 8)
	ch := make(chan Event, QueueCapacity)
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ch)
	}()

	ch <- Event{
		Kind: EventKindTrace,
		Trace: &TracePayload{
			Provider:   "anthropic",
			Model:      "claude-sonnet-4",
			InputText:  "0123456789abcdef",
			OutputText: "abcdefghijk",
			Status:     "ok",
		},
	}

	close(ch)
	if err := <-done; err != nil {
		t.Fatalf("worker returned error: %v", err)
	}

	traceID, input, output, err := dbm.LatestTraceTexts(context.Background())
	if err != nil {
		t.Fatalf("latest trace query failed: %v", err)
	}
	if len(traceID) != 36 {
		t.Fatalf("trace_id length = %d, want 36", len(traceID))
	}
	if len([]byte(input)) != 8 {
		t.Fatalf("input bytes = %d, want 8", len([]byte(input)))
	}
	if len([]byte(output)) != 8 {
		t.Fatalf("output bytes = %d, want 8", len([]byte(output)))
	}
}
