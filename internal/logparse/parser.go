package logparse

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/ingest"
)

type Enqueuer interface {
	Enqueue(event ingest.Event) bool
}

type Parser struct {
	path     string
	poll     time.Duration
	enqueuer Enqueuer
}

func New(path string, poll time.Duration, enqueuer Enqueuer) *Parser {
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	return &Parser{
		path:     path,
		poll:     poll,
		enqueuer: enqueuer,
	}
}

func (p *Parser) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.poll)
	defer ticker.Stop()

	var offset int64
	var lastInode uint64
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			fi, err := os.Stat(p.path)
			if err != nil {
				continue
			}
			stat, ok := fi.Sys().(*syscall.Stat_t)
			if ok {
				if lastInode == 0 {
					lastInode = stat.Ino
				}
				if stat.Ino != lastInode {
					lastInode = stat.Ino
					offset = 0
				}
			}
			if fi.Size() < offset {
				offset = 0
			}
			newOffset, err := p.readFromOffset(offset)
			if err != nil {
				continue
			}
			offset = newOffset
		}
	}
}

func (p *Parser) readFromOffset(offset int64) (int64, error) {
	f, err := os.Open(p.path)
	if err != nil {
		return offset, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, 0); err != nil {
		return offset, err
	}

	reader := bufio.NewScanner(f)
	for reader.Scan() {
		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}
		errorType, ok := classifyLine(line)
		if !ok {
			continue
		}
		meta, _ := json.Marshal(map[string]any{
			"source":   "log_parser",
			"raw_line": line,
		})
		p.enqueuer.Enqueue(ingest.Event{
			Kind:      ingest.EventKindError,
			CreatedAt: time.Now().UnixMilli(),
			Error: &ingest.ErrorPayload{
				ErrorType:  errorType,
				Message:    limit(line, 500),
				StackTrace: "",
				Severity:   "info",
				Metadata:   string(meta),
			},
		})
	}
	pos, _ := f.Seek(0, 1)
	return pos, reader.Err()
}

func classifyLine(line string) (string, bool) {
	l := strings.ToLower(line)
	if strings.Contains(l, "channel") {
		return "channel_event", true
	}
	if strings.Contains(l, "config") || strings.Contains(l, "reload") {
		return "config_change", true
	}
	if strings.Contains(l, "error") || strings.Contains(l, "exception") || strings.Contains(l, "timeout") || strings.Contains(l, "failed") {
		return "gateway_error", true
	}
	return "", false
}

func limit(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
