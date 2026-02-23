package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/config"
	"github.com/kon-rad/openclaw-trace/internal/db"
	"github.com/kon-rad/openclaw-trace/internal/ingest"
	"github.com/kon-rad/openclaw-trace/internal/logparse"
	"github.com/kon-rad/openclaw-trace/internal/metrics"
	"github.com/kon-rad/openclaw-trace/internal/push"
	"github.com/kon-rad/openclaw-trace/internal/server"
)

type Runtime struct {
	cfg        *config.Config
	logger     *slog.Logger
	version    string
	startedAt  time.Time
	dbm        *db.Manager
	httpServer *http.Server
	ingestCh   chan ingest.Event
	workerDone chan error
	bgCancel   context.CancelFunc
	bgWG       sync.WaitGroup
	pusher     *push.Pusher

	eventsReceived atomic.Int64
	eventsDropped  atomic.Int64
	lastPushTime   atomic.Int64
	lastPushStatus atomic.Value
}

func New(cfg *config.Config, logger *slog.Logger, version string) *Runtime {
	r := &Runtime{
		cfg:       cfg,
		logger:    logger,
		version:   version,
		startedAt: time.Now(),
	}
	r.lastPushStatus.Store("disabled")
	return r
}

func (r *Runtime) Run(ctx context.Context) error {
	dbm, err := db.Open(r.cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	r.dbm = dbm

	journalMode, busyTimeout, autoVacuum, err := r.dbm.Pragmas(ctx)
	if err != nil {
		return fmt.Errorf("query sqlite pragmas: %w", err)
	}
	r.logger.Info("SQLite opened",
		"path", r.cfg.DBPath,
		"journal_mode", journalMode,
		"busy_timeout", busyTimeout,
		"auto_vacuum", autoVacuum,
		"tables", 4,
	)

	healthHandler := server.NewHealthHandler(r.dbm, r.startedAt, r.version, r, r.cfg.PushEndpoint == "")
	r.ingestCh = make(chan ingest.Event, ingest.QueueCapacity)
	r.workerDone = make(chan error, 1)

	worker := ingest.NewWorker(r.logger, r.dbm, r.cfg.MaxTextBytes)
	go func() {
		r.workerDone <- worker.Run(r.ingestCh)
	}()

	if r.cfg.PushEndpoint != "" {
		r.pusher = push.New(r.dbm, r.cfg.PushEndpoint, r.cfg.PushMaxPayloadBytes)
		r.lastPushStatus.Store("ready")
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	r.bgCancel = bgCancel
	r.startBackgroundLoops(bgCtx)
	ingestHandlers := server.NewIngestHandlers(r)
	r.httpServer = server.New(":"+r.cfg.Port, healthHandler.ServeHTTP, ingestHandlers)

	serverErr := make(chan error, 1)
	go func() {
		r.logger.Info("Listening", "addr", ":"+r.cfg.Port)
		if err := r.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("http server failed: %w", err)
		}
		return nil
	case <-ctx.Done():
		r.logger.Info("SIGTERM received, shutting down...")
		return r.shutdown(context.Background())
	}
}

func (r *Runtime) Snapshot() server.RuntimeSnapshot {
	var lastPush *int64
	if ts := r.lastPushTime.Load(); ts > 0 {
		t := ts
		lastPush = &t
	}

	lastPushStatus := ""
	if s, ok := r.lastPushStatus.Load().(string); ok {
		lastPushStatus = s
	}

	return server.RuntimeSnapshot{
		QueueDepth:     int64(len(r.ingestCh)),
		EventsReceived: r.eventsReceived.Load(),
		EventsDropped:  r.eventsDropped.Load(),
		LastPushTime:   lastPush,
		LastPushStatus: lastPushStatus,
	}
}

func (r *Runtime) shutdown(ctx context.Context) error {
	var joined error
	r.logger.Info("Draining ingest channel", "remaining", len(r.ingestCh))

	if r.httpServer != nil {
		httpCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := r.httpServer.Shutdown(httpCtx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("http shutdown: %w", err))
		}
	}

	r.logger.Info("Final push", "status", "skipped", "reason", "not_implemented_phase_1")

	if r.bgCancel != nil {
		r.bgCancel()
		done := make(chan struct{})
		go func() {
			r.bgWG.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			joined = errors.Join(joined, errors.New("background loop shutdown timeout"))
		}
	}

	if r.ingestCh != nil {
		close(r.ingestCh)
		r.ingestCh = nil
	}
	if r.workerDone != nil {
		select {
		case err := <-r.workerDone:
			if err != nil {
				joined = errors.Join(joined, fmt.Errorf("worker shutdown: %w", err))
			}
		case <-time.After(5 * time.Second):
			joined = errors.Join(joined, errors.New("worker drain timeout"))
		}
	}

	if r.pusher != nil {
		pushCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := r.runPush(pushCtx, "shutdown")
		cancel()
		if err != nil {
			joined = errors.Join(joined, fmt.Errorf("final push: %w", err))
		}
	}

	if r.dbm != nil {
		cpCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := r.dbm.Checkpoint(cpCtx); err != nil {
			r.logger.Warn("WAL checkpoint failed", "error", err)
			joined = errors.Join(joined, fmt.Errorf("wal checkpoint: %w", err))
		}
		if err := r.dbm.Close(); err != nil {
			joined = errors.Join(joined, fmt.Errorf("db close: %w", err))
		}
	}

	r.logger.Info("Shutdown complete",
		"total_events", r.eventsReceived.Load(),
		"uptime", time.Since(r.startedAt).String(),
	)
	return joined
}

func (r *Runtime) Enqueue(event ingest.Event) bool {
	if r.ingestCh == nil {
		r.eventsDropped.Add(1)
		return false
	}
	if ingest.TryEnqueue(r.ingestCh, event) {
		r.eventsReceived.Add(1)
		return true
	}
	r.eventsDropped.Add(1)
	return false
}

func (r *Runtime) startBackgroundLoops(ctx context.Context) {
	collector := metrics.NewCollector(r.cfg.MetricsInterval, r, r.cfg.DBPath)
	r.bgWG.Add(1)
	go func() {
		defer r.bgWG.Done()
		if err := collector.Run(ctx); err != nil {
			r.logger.Warn("metrics collector stopped", "error", err)
		}
	}()

	if r.cfg.LogPath != "" {
		r.bgWG.Add(1)
		go func() {
			defer r.bgWG.Done()
			parser := logparse.New(r.cfg.LogPath, 500*time.Millisecond, r)
			if err := parser.Run(ctx); err != nil {
				r.logger.Warn("log parser stopped", "error", err)
			}
		}()
	}

	if r.pusher != nil {
		r.bgWG.Add(1)
		go func() {
			defer r.bgWG.Done()
			ticker := time.NewTicker(r.cfg.PushInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					pushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					_ = r.runPush(pushCtx, "scheduled")
					cancel()
				}
			}
		}()
	}

	r.bgWG.Add(1)
	go func() {
		defer r.bgWG.Done()
		ticker := time.NewTicker(r.cfg.CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_, _, err := r.dbm.CleanupOldSynced(
					cleanupCtx,
					r.cfg.RetentionDays,
					r.cfg.CleanupDiskThreshold,
					r.cfg.CleanupDBThresholdByte,
				)
				cancel()
				if err != nil {
					r.logger.Warn("cleanup failed", "error", err)
				}
			}
		}
	}()

	r.bgWG.Add(1)
	go func() {
		defer r.bgWG.Done()
		ticker := time.NewTicker(r.cfg.WALCheckpointInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cpCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_, err := r.dbm.CheckpointIfWALExceeds(cpCtx, r.cfg.WALRestartThresholdB)
				cancel()
				if err != nil {
					r.logger.Warn("wal checkpoint loop failed", "error", err)
				}
			}
		}
	}()
}

func (r *Runtime) runPush(ctx context.Context, reason string) error {
	if r.pusher == nil {
		return nil
	}
	res, err := r.pusher.PushOnce(ctx)
	if err != nil {
		r.lastPushStatus.Store("error")
		r.logger.Warn("push failed", "reason", reason, "error", err)
		return err
	}
	r.lastPushStatus.Store("ok")
	r.lastPushTime.Store(time.Now().UnixMilli())
	r.logger.Info("push completed", "reason", reason, "batches", res.BatchesSent, "events", res.EventsSent)
	return nil
}
