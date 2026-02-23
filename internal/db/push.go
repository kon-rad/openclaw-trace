package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type PushEvent struct {
	TableName string
	RowID     int64
	CreatedAt int64
	TraceID   string
	Type      string
	Data      json.RawMessage
}

func (m *Manager) FetchUnsyncedEvents(ctx context.Context, limit int) ([]PushEvent, error) {
	query := `
SELECT table_name, id, created_at, trace_id, event_type, payload
FROM (
  SELECT 'llm_traces' AS table_name, id, created_at, trace_id, 'llm_trace' AS event_type,
    json_object(
      'trace_id', trace_id,
      'created_at', created_at,
      'provider', provider,
      'model', model,
      'input_text', input_text,
      'output_text', output_text,
      'prompt_tokens', prompt_tokens,
      'completion_tokens', completion_tokens,
      'total_tokens', total_tokens,
      'cost_usd', cost_usd,
      'latency_ms', latency_ms,
      'status', status,
      'error_type', error_type,
      'metadata', metadata
    ) AS payload
  FROM llm_traces WHERE synced = 0
  UNION ALL
  SELECT 'error_events' AS table_name, id, created_at, trace_id, 'error_event' AS event_type,
    json_object(
      'trace_id', trace_id,
      'created_at', created_at,
      'error_type', error_type,
      'message', message,
      'stack_trace', stack_trace,
      'severity', severity,
      'metadata', metadata
    ) AS payload
  FROM error_events WHERE synced = 0
  UNION ALL
  SELECT 'system_metrics' AS table_name, id, created_at, trace_id, 'system_metric' AS event_type,
    json_object(
      'trace_id', trace_id,
      'created_at', created_at,
      'cpu_pct', cpu_pct,
      'mem_rss_bytes', mem_rss_bytes,
      'mem_available', mem_available,
      'mem_total', mem_total,
      'disk_used_bytes', disk_used_bytes,
      'disk_total_bytes', disk_total_bytes,
      'disk_free_bytes', disk_free_bytes,
      'metadata', metadata
    ) AS payload
  FROM system_metrics WHERE synced = 0
)
ORDER BY created_at ASC
LIMIT ?;
`
	rows, err := m.reader.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]PushEvent, 0, limit)
	for rows.Next() {
		var ev PushEvent
		var payload string
		if err := rows.Scan(&ev.TableName, &ev.RowID, &ev.CreatedAt, &ev.TraceID, &ev.Type, &payload); err != nil {
			return nil, err
		}
		ev.Data = json.RawMessage(payload)
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (m *Manager) MarkEventsSynced(ctx context.Context, events []PushEvent, pushedAt int64) error {
	grouped := map[string][]int64{
		"llm_traces":     {},
		"error_events":   {},
		"system_metrics": {},
	}
	for _, ev := range events {
		grouped[ev.TableName] = append(grouped[ev.TableName], ev.RowID)
	}

	tx, err := m.writer.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for table, ids := range grouped {
		if len(ids) == 0 {
			continue
		}
		placeholders := make([]string, 0, len(ids))
		args := make([]any, 0, len(ids)+1)
		args = append(args, pushedAt)
		for _, id := range ids {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		sqlQ := fmt.Sprintf("UPDATE %s SET synced = 1, pushed_at = ? WHERE id IN (%s)", table, strings.Join(placeholders, ","))
		if _, err := tx.ExecContext(ctx, sqlQ, args...); err != nil {
			return err
		}
	}

	_, _ = tx.ExecContext(ctx, "INSERT INTO push_log (created_at, status, events_pushed, duration_ms) VALUES (?, 'ok', ?, 0)", time.Now().UnixMilli(), len(events))

	return tx.Commit()
}

func (m *Manager) PendingCounts(ctx context.Context) (traces int64, errs int64, metrics int64, err error) {
	if err = m.reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM llm_traces WHERE synced = 0").Scan(&traces); err != nil {
		return
	}
	if err = m.reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM error_events WHERE synced = 0").Scan(&errs); err != nil {
		return
	}
	if err = m.reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM system_metrics WHERE synced = 0").Scan(&metrics); err != nil {
		return
	}
	return
}
