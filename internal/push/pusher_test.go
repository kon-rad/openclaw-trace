package push

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/db"
)

type mockTransport struct {
	statusCode int
	requests   int64
	eventsSeen int64
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	if events, ok := payload["events"].([]any); ok {
		atomic.AddInt64(&m.eventsSeen, int64(len(events)))
	}
	atomic.AddInt64(&m.requests, 1)
	return &http.Response{
		StatusCode: m.statusCode,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
		Header:     make(http.Header),
	}, nil
}

func seedEvents(t *testing.T, dbm *db.Manager, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		traceID := "00000000-0000-4000-8000-00000000000" + string(rune('a'+(i%26)))
		err := dbm.InsertBatch(context.Background(),
			[]db.TraceInsert{{
				TraceID:    traceID,
				CreatedAt:  time.Now().UnixMilli() + int64(i),
				Provider:   "anthropic",
				Model:      "claude",
				InputText:  "input",
				OutputText: "output",
				Status:     "ok",
			}},
			[]db.ErrorInsert{{
				TraceID:   traceID,
				CreatedAt: time.Now().UnixMilli() + int64(i),
				ErrorType: "llm_error",
				Message:   "m",
				Severity:  "error",
			}},
			[]db.MetricInsert{{
				TraceID:       traceID,
				CreatedAt:     time.Now().UnixMilli() + int64(i),
				CPUPct:        10,
				MemRSSBytes:   1,
				MemAvailable:  1,
				MemTotal:      2,
				DiskUsedBytes: 1,
				DiskTotal:     2,
				DiskFreeBytes: 1,
			}},
		)
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}
}

func TestPushOnceSuccessMarksSynced(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()
	seedEvents(t, dbm, 1)

	transport := &mockTransport{statusCode: http.StatusOK}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}

	p := New(dbm, "http://push.local/v1/ingest", 5*1024*1024)
	p.SetTestOptions(client, 2, 1*time.Millisecond)

	res, err := p.PushOnce(context.Background())
	if err != nil {
		t.Fatalf("push once failed: %v", err)
	}
	if res.EventsSent == 0 || atomic.LoadInt64(&transport.eventsSeen) == 0 {
		t.Fatalf("expected pushed events")
	}

	tr, er, mr, err := dbm.PendingCounts(context.Background())
	if err != nil {
		t.Fatalf("pending counts: %v", err)
	}
	if tr != 0 || er != 0 || mr != 0 {
		t.Fatalf("expected all synced, got traces=%d errors=%d metrics=%d", tr, er, mr)
	}
}

func TestPushOnceFailureKeepsUnsynced(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()
	seedEvents(t, dbm, 1)

	transport := &mockTransport{statusCode: http.StatusInternalServerError}
	client := &http.Client{Transport: transport, Timeout: 1 * time.Second}

	p := New(dbm, "http://push.local/v1/ingest", 5*1024*1024)
	p.SetTestOptions(client, 2, 1*time.Millisecond)

	if _, err := p.PushOnce(context.Background()); err == nil {
		t.Fatalf("expected push failure")
	}

	tr, er, mr, err := dbm.PendingCounts(context.Background())
	if err != nil {
		t.Fatalf("pending counts: %v", err)
	}
	if tr == 0 || er == 0 || mr == 0 {
		t.Fatalf("expected pending rows after failed push, got traces=%d errors=%d metrics=%d", tr, er, mr)
	}
}

func TestPushSplitsPayloadByMaxBytes(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()
	seedEvents(t, dbm, 8)

	transport := &mockTransport{statusCode: http.StatusOK}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}

	p := New(dbm, "http://push.local/v1/ingest", 600)
	p.SetTestOptions(client, 2, 1*time.Millisecond)

	res, err := p.PushOnce(context.Background())
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}
	if res.BatchesSent < 2 || atomic.LoadInt64(&transport.requests) < 2 {
		t.Fatalf("expected split batches, got %d requests", atomic.LoadInt64(&transport.requests))
	}
}
