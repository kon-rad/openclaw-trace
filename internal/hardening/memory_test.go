package hardening

import (
	"runtime"
	"testing"
)

func TestCurrentRSSBytes(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("linux-only rss probe")
	}
	rss, err := CurrentRSSBytes()
	if err != nil {
		t.Fatalf("CurrentRSSBytes() error: %v", err)
	}
	if rss <= 0 {
		t.Fatalf("rss bytes should be > 0")
	}
}
