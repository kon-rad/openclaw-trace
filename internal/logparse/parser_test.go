package logparse

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/ingest"
)

type memEnqueuer struct {
	mu     sync.Mutex
	events []ingest.Event
}

func (m *memEnqueuer) Enqueue(event ingest.Event) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return true
}

func (m *memEnqueuer) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func (m *memEnqueuer) ErrorTypes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.events))
	for _, e := range m.events {
		if e.Error != nil {
			out = append(out, e.Error.ErrorType)
		}
	}
	return out
}

func TestParserEmitsEventsForRelevantLines(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "gateway.log")
	if err := os.WriteFile(logPath, []byte(""), 0o644); err != nil {
		t.Fatalf("create log file: %v", err)
	}

	mem := &memEnqueuer{}
	parser := New(logPath, 50*time.Millisecond, mem)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = parser.Run(ctx) }()

	lines := "channel connected\nnormal info line\nconfig reloaded\nrequest failed timeout\n"
	if err := os.WriteFile(logPath, []byte(lines), 0o644); err != nil {
		t.Fatalf("write log lines: %v", err)
	}

	time.Sleep(250 * time.Millisecond)
	got := mem.Count()
	if got < 3 {
		t.Fatalf("expected at least 3 parsed events, got %d", got)
	}
}

func TestParserHandlesRotation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "gateway.log")
	if err := os.WriteFile(logPath, []byte("channel one\n"), 0o644); err != nil {
		t.Fatalf("create log file: %v", err)
	}

	mem := &memEnqueuer{}
	parser := New(logPath, 50*time.Millisecond, mem)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = parser.Run(ctx) }()

	time.Sleep(150 * time.Millisecond)

	rotated := filepath.Join(dir, "gateway.log.1")
	if err := os.Rename(logPath, rotated); err != nil {
		t.Fatalf("rotate log: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("config changed\n"), 0o644); err != nil {
		t.Fatalf("create new log: %v", err)
	}

	time.Sleep(250 * time.Millisecond)
	types := mem.ErrorTypes()
	if len(types) < 2 {
		t.Fatalf("expected parser events before+after rotation, got %d", len(types))
	}
}
