package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/db"
	"github.com/kon-rad/openclaw-trace/internal/ingest"
)

type chanEnqueuer struct {
	ch chan ingest.Event
}

func (e chanEnqueuer) Enqueue(event ingest.Event) bool {
	return ingest.TryEnqueue(e.ch, event)
}

func TestPostTraceAcceptedAndPersisted(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()

	ch := make(chan ingest.Event, ingest.QueueCapacity)
	worker := ingest.NewWorker(slog.New(slog.NewJSONHandler(io.Discard, nil)), dbm, 1024)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ch) }()

	h := NewIngestHandlers(chanEnqueuer{ch: ch})
	body, _ := json.Marshal(map[string]any{
		"provider":          "anthropic",
		"model":             "claude-sonnet-4",
		"input_text":        "hello",
		"output_text":       "world",
		"prompt_tokens":     10,
		"completion_tokens": 20,
		"total_tokens":      30,
		"cost_usd":          0.12,
		"latency_ms":        150,
		"status":            "ok",
		"error_type":        "",
		"metadata":          "{\"req\":\"1\"}",
	})

	start := time.Now()
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PostTrace(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status code = %d, want 202", rec.Code)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("handler took too long: %s", elapsed)
	}

	close(ch)
	if err := <-done; err != nil {
		t.Fatalf("worker error: %v", err)
	}

	row, err := dbm.LatestTrace(context.Background())
	if err != nil {
		t.Fatalf("query latest trace: %v", err)
	}
	if row.Provider != "anthropic" || row.Model != "claude-sonnet-4" {
		t.Fatalf("unexpected provider/model: %s/%s", row.Provider, row.Model)
	}
	if row.TotalTokens != 30 || row.PromptTokens != 10 || row.CompletionTokens != 20 {
		t.Fatalf("unexpected token capture: %+v", row)
	}
	if row.Status != "ok" || row.LatencyMS != 150 {
		t.Fatalf("unexpected status/latency: %+v", row)
	}
}

func TestPostErrorAcceptedAndPersistsAllTypes(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()

	ch := make(chan ingest.Event, ingest.QueueCapacity)
	worker := ingest.NewWorker(slog.New(slog.NewJSONHandler(io.Discard, nil)), dbm, 1024)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ch) }()

	h := NewIngestHandlers(chanEnqueuer{ch: ch})
	errorTypes := []string{"llm_error", "crash", "system_error"}
	for _, et := range errorTypes {
		body, _ := json.Marshal(map[string]any{
			"error_type":  et,
			"message":     "msg-" + et,
			"stack_trace": "trace",
			"severity":    "error",
			"metadata":    "{\"k\":\"v\"}",
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/errors", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.PostError(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status for %s = %d, want 202", et, rec.Code)
		}
	}

	close(ch)
	if err := <-done; err != nil {
		t.Fatalf("worker error: %v", err)
	}

	for _, et := range errorTypes {
		count, err := dbm.ErrorCountByType(context.Background(), et)
		if err != nil {
			t.Fatalf("query error type count failed: %v", err)
		}
		if count != 1 {
			t.Fatalf("error count for %s = %d, want 1", et, count)
		}
	}
}

func TestPostTraceQueueSaturationStillReturns202(t *testing.T) {
	t.Parallel()

	ch := make(chan ingest.Event, 1)
	h := NewIngestHandlers(chanEnqueuer{ch: ch})

	body, _ := json.Marshal(map[string]any{
		"provider": "anthropic",
		"model":    "claude-sonnet-4",
	})

	req1 := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	rec1 := httptest.NewRecorder()
	h.PostTrace(rec1, req1)
	if rec1.Code != http.StatusAccepted {
		t.Fatalf("first post status = %d, want 202", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	h.PostTrace(rec2, req2)
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("second post status = %d, want 202 even when saturated", rec2.Code)
	}
}
