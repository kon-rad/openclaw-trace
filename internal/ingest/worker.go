package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/kon-rad/openclaw-trace/internal/db"
)

type Worker struct {
	logger       *slog.Logger
	dbm          *db.Manager
	maxTextBytes int
}

func NewWorker(logger *slog.Logger, dbm *db.Manager, maxTextBytes int) *Worker {
	return &Worker{
		logger:       logger,
		dbm:          dbm,
		maxTextBytes: maxTextBytes,
	}
}

func (w *Worker) Run(events <-chan Event) error {
	ticker := time.NewTicker(FlushWindow)
	defer ticker.Stop()

	buffer := make([]Event, 0, MaxBatchSize)

	flush := func(batch []Event) error {
		if len(batch) == 0 {
			return nil
		}
		traces := make([]db.TraceInsert, 0, len(batch))
		errs := make([]db.ErrorInsert, 0, len(batch))
		metrics := make([]db.MetricInsert, 0, len(batch))
		for _, ev := range batch {
			traceID := uuid.NewString()
			createdAt := ev.CreatedAt
			if createdAt == 0 {
				createdAt = time.Now().UnixMilli()
			}
			switch ev.Kind {
			case EventKindTrace:
				if ev.Trace == nil {
					continue
				}
				traces = append(traces, db.TraceInsert{
					TraceID:          traceID,
					CreatedAt:        createdAt,
					Provider:         ev.Trace.Provider,
					Model:            ev.Trace.Model,
					InputText:        TruncateBytes(ev.Trace.InputText, w.maxTextBytes),
					OutputText:       TruncateBytes(ev.Trace.OutputText, w.maxTextBytes),
					PromptTokens:     ev.Trace.PromptTokens,
					CompletionTokens: ev.Trace.CompletionTokens,
					TotalTokens:      ev.Trace.TotalTokens,
					CostUSD:          ev.Trace.CostUSD,
					LatencyMS:        ev.Trace.LatencyMS,
					Status:           ev.Trace.Status,
					ErrorType:        ev.Trace.ErrorType,
					Metadata:         ev.Trace.Metadata,
				})
			case EventKindError:
				if ev.Error == nil {
					continue
				}
				errs = append(errs, db.ErrorInsert{
					TraceID:    traceID,
					CreatedAt:  createdAt,
					ErrorType:  ev.Error.ErrorType,
					Message:    ev.Error.Message,
					StackTrace: ev.Error.StackTrace,
					Severity:   ev.Error.Severity,
					Metadata:   ev.Error.Metadata,
				})
			case EventKindMetric:
				if ev.Metric == nil {
					continue
				}
				metrics = append(metrics, db.MetricInsert{
					TraceID:       traceID,
					CreatedAt:     createdAt,
					CPUPct:        ev.Metric.CPUPct,
					MemRSSBytes:   ev.Metric.MemRSSBytes,
					MemAvailable:  ev.Metric.MemAvailable,
					MemTotal:      ev.Metric.MemTotal,
					DiskUsedBytes: ev.Metric.DiskUsedBytes,
					DiskTotal:     ev.Metric.DiskTotal,
					DiskFreeBytes: ev.Metric.DiskFreeBytes,
					Metadata:      ev.Metric.Metadata,
				})
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := w.dbm.InsertBatch(ctx, traces, errs, metrics); err != nil {
			return fmt.Errorf("insert batch: %w", err)
		}
		return nil
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return flush(buffer)
			}
			buffer = append(buffer, ev)
			if len(buffer) >= MaxBatchSize {
				if err := flush(buffer); err != nil {
					w.logger.Error("ingest flush failed", "error", err)
					return err
				}
				buffer = buffer[:0]
			}
		case <-ticker.C:
			if len(buffer) == 0 {
				continue
			}
			if err := flush(buffer); err != nil {
				w.logger.Error("ingest timed flush failed", "error", err)
				return err
			}
			buffer = buffer[:0]
		}
	}
}
