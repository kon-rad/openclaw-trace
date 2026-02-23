package db

const schemaDDL = `
CREATE TABLE IF NOT EXISTS llm_traces (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  trace_id TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  input_text TEXT,
  output_text TEXT,
  prompt_tokens INTEGER,
  completion_tokens INTEGER,
  total_tokens INTEGER,
  cost_usd REAL,
  latency_ms INTEGER,
  status TEXT NOT NULL DEFAULT 'ok',
  error_type TEXT,
  metadata TEXT,
  synced INTEGER NOT NULL DEFAULT 0,
  pushed_at INTEGER
);

CREATE TABLE IF NOT EXISTS error_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  trace_id TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  error_type TEXT NOT NULL,
  message TEXT NOT NULL,
  stack_trace TEXT,
  severity TEXT NOT NULL DEFAULT 'error',
  metadata TEXT,
  synced INTEGER NOT NULL DEFAULT 0,
  pushed_at INTEGER
);

CREATE TABLE IF NOT EXISTS system_metrics (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  trace_id TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  cpu_pct REAL,
  mem_rss_bytes INTEGER,
  mem_available INTEGER,
  mem_total INTEGER,
  disk_used_bytes INTEGER,
  disk_total_bytes INTEGER,
  disk_free_bytes INTEGER,
  metadata TEXT,
  synced INTEGER NOT NULL DEFAULT 0,
  pushed_at INTEGER
);

CREATE TABLE IF NOT EXISTS push_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at INTEGER NOT NULL,
  status TEXT NOT NULL,
  events_pushed INTEGER NOT NULL DEFAULT 0,
  error_message TEXT,
  duration_ms INTEGER
);

CREATE INDEX IF NOT EXISTS idx_llm_synced ON llm_traces (synced, created_at);
CREATE INDEX IF NOT EXISTS idx_error_synced ON error_events (synced, created_at);
CREATE INDEX IF NOT EXISTS idx_metrics_synced ON system_metrics (synced, created_at);
`
