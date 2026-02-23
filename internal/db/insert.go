package db

import (
	"context"
	"database/sql"
	"fmt"
)

type TraceInsert struct {
	TraceID          string
	CreatedAt        int64
	Provider         string
	Model            string
	InputText        string
	OutputText       string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostUSD          float64
	LatencyMS        int
	Status           string
	ErrorType        string
	Metadata         string
}

type ErrorInsert struct {
	TraceID    string
	CreatedAt  int64
	ErrorType  string
	Message    string
	StackTrace string
	Severity   string
	Metadata   string
}

type MetricInsert struct {
	TraceID       string
	CreatedAt     int64
	CPUPct        float64
	MemRSSBytes   int64
	MemAvailable  int64
	MemTotal      int64
	DiskUsedBytes int64
	DiskTotal     int64
	DiskFreeBytes int64
	Metadata      string
}

type TraceRow struct {
	TraceID          string
	Provider         string
	Model            string
	InputText        string
	OutputText       string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostUSD          float64
	LatencyMS        int
	Status           string
	ErrorType        string
	Metadata         string
}

type ErrorRow struct {
	TraceID    string
	ErrorType  string
	Message    string
	StackTrace string
	Severity   string
	Metadata   string
}

func (m *Manager) InsertBatch(ctx context.Context, traces []TraceInsert, errs []ErrorInsert, metrics []MetricInsert) error {
	tx, err := m.writer.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if len(traces) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
INSERT INTO llm_traces (
  trace_id, created_at, provider, model, input_text, output_text,
  prompt_tokens, completion_tokens, total_tokens, cost_usd, latency_ms,
  status, error_type, metadata, synced, pushed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL)
`)
		if err != nil {
			return fmt.Errorf("prepare trace insert: %w", err)
		}
		defer stmt.Close()

		for _, row := range traces {
			if _, err := stmt.ExecContext(
				ctx,
				row.TraceID,
				row.CreatedAt,
				row.Provider,
				row.Model,
				row.InputText,
				row.OutputText,
				row.PromptTokens,
				row.CompletionTokens,
				row.TotalTokens,
				row.CostUSD,
				row.LatencyMS,
				row.Status,
				row.ErrorType,
				row.Metadata,
			); err != nil {
				return fmt.Errorf("insert trace row: %w", err)
			}
		}
	}

	if len(errs) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
INSERT INTO error_events (
  trace_id, created_at, error_type, message, stack_trace, severity, metadata, synced, pushed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 0, NULL)
`)
		if err != nil {
			return fmt.Errorf("prepare error insert: %w", err)
		}
		defer stmt.Close()

		for _, row := range errs {
			if _, err := stmt.ExecContext(
				ctx,
				row.TraceID,
				row.CreatedAt,
				row.ErrorType,
				row.Message,
				row.StackTrace,
				row.Severity,
				row.Metadata,
			); err != nil {
				return fmt.Errorf("insert error row: %w", err)
			}
		}
	}

	if len(metrics) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
INSERT INTO system_metrics (
  trace_id, created_at, cpu_pct, mem_rss_bytes, mem_available, mem_total,
  disk_used_bytes, disk_total_bytes, disk_free_bytes, metadata, synced, pushed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL)
`)
		if err != nil {
			return fmt.Errorf("prepare metric insert: %w", err)
		}
		defer stmt.Close()

		for _, row := range metrics {
			if _, err := stmt.ExecContext(
				ctx,
				row.TraceID,
				row.CreatedAt,
				row.CPUPct,
				row.MemRSSBytes,
				row.MemAvailable,
				row.MemTotal,
				row.DiskUsedBytes,
				row.DiskTotal,
				row.DiskFreeBytes,
				row.Metadata,
			); err != nil {
				return fmt.Errorf("insert metric row: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (m *Manager) TraceCount(ctx context.Context) (int64, error) {
	var out int64
	if err := m.reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM llm_traces").Scan(&out); err != nil {
		return 0, err
	}
	return out, nil
}

func (m *Manager) ErrorCount(ctx context.Context) (int64, error) {
	var out int64
	if err := m.reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM error_events").Scan(&out); err != nil {
		return 0, err
	}
	return out, nil
}

func (m *Manager) MetricCount(ctx context.Context) (int64, error) {
	var out int64
	if err := m.reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM system_metrics").Scan(&out); err != nil {
		return 0, err
	}
	return out, nil
}

func (m *Manager) LatestTraceTexts(ctx context.Context) (traceID string, input string, output string, err error) {
	err = m.reader.QueryRowContext(
		ctx,
		`SELECT trace_id, COALESCE(input_text,''), COALESCE(output_text,'') FROM llm_traces ORDER BY id DESC LIMIT 1`,
	).Scan(&traceID, &input, &output)
	return
}

func (m *Manager) LatestTrace(ctx context.Context) (TraceRow, error) {
	var row TraceRow
	err := m.reader.QueryRowContext(ctx, `
SELECT trace_id, provider, model, COALESCE(input_text,''), COALESCE(output_text,''), prompt_tokens, completion_tokens, total_tokens, cost_usd, latency_ms, status, COALESCE(error_type,''), COALESCE(metadata,'')
FROM llm_traces
ORDER BY id DESC LIMIT 1
`).Scan(
		&row.TraceID,
		&row.Provider,
		&row.Model,
		&row.InputText,
		&row.OutputText,
		&row.PromptTokens,
		&row.CompletionTokens,
		&row.TotalTokens,
		&row.CostUSD,
		&row.LatencyMS,
		&row.Status,
		&row.ErrorType,
		&row.Metadata,
	)
	return row, err
}

func (m *Manager) LatestError(ctx context.Context) (ErrorRow, error) {
	var row ErrorRow
	err := m.reader.QueryRowContext(ctx, `
SELECT trace_id, error_type, message, COALESCE(stack_trace,''), severity, COALESCE(metadata,'')
FROM error_events
ORDER BY id DESC LIMIT 1
`).Scan(
		&row.TraceID,
		&row.ErrorType,
		&row.Message,
		&row.StackTrace,
		&row.Severity,
		&row.Metadata,
	)
	return row, err
}

func (m *Manager) ErrorCountByType(ctx context.Context, errorType string) (int64, error) {
	var out int64
	if err := m.reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM error_events WHERE error_type = ?", errorType).Scan(&out); err != nil {
		return 0, err
	}
	return out, nil
}
