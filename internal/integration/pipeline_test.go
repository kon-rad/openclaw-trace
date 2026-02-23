package integration

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/db"
	"github.com/kon-rad/openclaw-trace/internal/push"
)

func TestPushPipeline100Traces(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	dbm, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = dbm.Close() }()

	for i := 0; i < 100; i++ {
		traceID := makeTraceID(i)
		if err := dbm.InsertBatch(context.Background(),
			[]db.TraceInsert{{
				TraceID:          traceID,
				CreatedAt:        time.Now().UnixMilli() + int64(i),
				Provider:         "anthropic",
				Model:            "claude-sonnet-4",
				InputText:        "input",
				OutputText:       "output",
				TotalTokens:      42,
				PromptTokens:     21,
				CompletionTokens: 21,
				Status:           "ok",
			}},
			nil,
			nil,
		); err != nil {
			t.Fatalf("seed trace insert %d: %v", i, err)
		}
	}

	var received int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Events []struct {
				Type string                 `json:"type"`
				Data map[string]interface{} `json:"data"`
			} `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		for _, ev := range payload.Events {
			if _, ok := ev.Data["trace_id"]; !ok {
				t.Fatalf("trace_id missing in pushed event")
			}
			atomic.AddInt64(&received, 1)
		}
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("network listener unavailable in sandbox: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = ln
	server.Start()
	defer server.Close()

	pusher := push.New(dbm, server.URL, 5*1024*1024)
	pusher.SetTestOptions(&http.Client{Timeout: 2 * time.Second}, 2, 1*time.Millisecond)

	res, err := pusher.PushOnce(context.Background())
	if err != nil {
		t.Fatalf("push once failed: %v", err)
	}
	if res.EventsSent < 100 {
		t.Fatalf("expected at least 100 pushed events, got %d", res.EventsSent)
	}
	if atomic.LoadInt64(&received) < 100 {
		t.Fatalf("receiver got %d events, want >=100", received)
	}

	traces, errs, metrics, err := dbm.PendingCounts(context.Background())
	if err != nil {
		t.Fatalf("pending counts: %v", err)
	}
	if traces != 0 || errs != 0 || metrics != 0 {
		t.Fatalf("expected no pending rows after successful push, got t=%d e=%d m=%d", traces, errs, metrics)
	}
}

func makeTraceID(i int) string {
	return "00000000-0000-4000-8000-" + leftPad(i, 12)
}

func leftPad(v int, width int) string {
	s := "000000000000" + strconv.Itoa(v)
	return s[len(s)-width:]
}
