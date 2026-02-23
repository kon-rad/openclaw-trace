package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/db"
)

type staticSnapshot struct{}

func (staticSnapshot) Snapshot() RuntimeSnapshot {
	return RuntimeSnapshot{
		QueueDepth:     0,
		EventsReceived: 0,
		EventsDropped:  0,
		LastPushStatus: "disabled",
	}
}

func TestHealthAlwaysReturnsContract(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		_ = dbm.Close()
	}()

	handler := NewHealthHandler(dbm, time.Now().Add(-5*time.Second), "test-version", staticSnapshot{}, true)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode error = %v", err)
	}

	required := []string{
		"status",
		"uptime_seconds",
		"version",
		"db_status",
		"db_size_bytes",
		"wal_size_bytes",
		"queue_depth",
		"events_received",
		"events_dropped",
		"last_push_time",
		"last_push_status",
		"unsynced_count",
	}
	for _, key := range required {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing health field %q", key)
		}
	}
}
