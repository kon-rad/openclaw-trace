package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/db"
)

type RuntimeSnapshot struct {
	QueueDepth     int64
	EventsReceived int64
	EventsDropped  int64
	LastPushTime   *int64
	LastPushStatus string
}

type SnapshotProvider interface {
	Snapshot() RuntimeSnapshot
}

type HealthResponse struct {
	Status         string   `json:"status"`
	UptimeSeconds  int64    `json:"uptime_seconds"`
	Version        string   `json:"version"`
	DBStatus       string   `json:"db_status"`
	DBSizeBytes    int64    `json:"db_size_bytes"`
	WALSizeBytes   int64    `json:"wal_size_bytes"`
	QueueDepth     int64    `json:"queue_depth"`
	EventsReceived int64    `json:"events_received"`
	EventsDropped  int64    `json:"events_dropped"`
	LastPushTime   *int64   `json:"last_push_time"`
	LastPushStatus string   `json:"last_push_status"`
	UnsyncedCount  int64    `json:"unsynced_count"`
	GeneratedAt    string   `json:"generated_at"`
	Warnings       []string `json:"warnings,omitempty"`
}

type HealthHandler struct {
	dbm          *db.Manager
	startTime    time.Time
	version      string
	snapshotter  SnapshotProvider
	pushDisabled bool
}

func NewHealthHandler(dbm *db.Manager, start time.Time, version string, snapshotter SnapshotProvider, pushDisabled bool) *HealthHandler {
	return &HealthHandler{
		dbm:          dbm,
		startTime:    start,
		version:      version,
		snapshotter:  snapshotter,
		pushDisabled: pushDisabled,
	}
}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	snapshot := h.snapshotter.Snapshot()
	dbStats := h.dbm.Stats()
	unsynced, err := h.dbm.UnsyncedCount(context.Background())

	resp := HealthResponse{
		Status:         "ok",
		UptimeSeconds:  int64(time.Since(h.startTime).Seconds()),
		Version:        h.version,
		DBStatus:       dbStats.DBStatus,
		DBSizeBytes:    dbStats.DBSizeBytes,
		WALSizeBytes:   dbStats.WALSize,
		QueueDepth:     snapshot.QueueDepth,
		EventsReceived: snapshot.EventsReceived,
		EventsDropped:  snapshot.EventsDropped,
		LastPushTime:   snapshot.LastPushTime,
		LastPushStatus: snapshot.LastPushStatus,
		UnsyncedCount:  unsynced,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	if h.pushDisabled && resp.LastPushStatus == "" {
		resp.LastPushStatus = "disabled"
	}
	if err != nil {
		resp.Status = "degraded"
		resp.Warnings = append(resp.Warnings, "unsynced_count_unavailable")
		resp.UnsyncedCount = 0
	}
	if resp.DBStatus != "ok" {
		resp.Status = "degraded"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
