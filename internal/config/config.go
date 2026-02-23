package config

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	Port                   string        `env:"OCT_PORT,default=9090"`
	DBPath                 string        `env:"OCT_DB_PATH,default=/data/openclaw-trace.db"`
	LogLevel               string        `env:"OCT_LOG_LEVEL,default=info"`
	PushEndpoint           string        `env:"OCT_PUSH_ENDPOINT"`
	PushInterval           time.Duration `env:"OCT_PUSH_INTERVAL,default=5m"`
	PushMaxPayloadBytes    int           `env:"OCT_PUSH_MAX_PAYLOAD_BYTES,default=5242880"`
	LogPath                string        `env:"OCT_LOG_PATH"`
	RetentionDays          int           `env:"OCT_RETENTION_DAYS,default=3"`
	MaxTextBytes           int           `env:"OCT_MAX_TEXT_BYTES,default=16384"`
	MetricsInterval        time.Duration `env:"OCT_METRICS_INTERVAL,default=15s"`
	CleanupInterval        time.Duration `env:"OCT_CLEANUP_INTERVAL,default=5m"`
	WALCheckpointInterval  time.Duration `env:"OCT_WAL_CHECKPOINT_INTERVAL,default=10m"`
	WALRestartThresholdB   int64         `env:"OCT_WAL_RESTART_THRESHOLD_BYTES,default=52428800"`
	CleanupDiskThreshold   float64       `env:"OCT_CLEANUP_DISK_THRESHOLD,default=80"`
	CleanupDBThresholdByte int64         `env:"OCT_CLEANUP_DB_THRESHOLD_BYTES,default=104857600"`
}

func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("load env config: %w", err)
	}
	return &cfg, nil
}

func WriteHelp(w io.Writer, version string) {
	fmt.Fprintf(w, "openclaw-trace %s\n\n", version)
	fmt.Fprintln(w, "Environment variables:")
	fmt.Fprintln(w, "  OCT_PORT=9090")
	fmt.Fprintln(w, "  OCT_DB_PATH=/data/openclaw-trace.db")
	fmt.Fprintln(w, "  OCT_LOG_LEVEL=info")
	fmt.Fprintln(w, "  OCT_PUSH_ENDPOINT=")
	fmt.Fprintln(w, "  OCT_PUSH_INTERVAL=5m")
	fmt.Fprintln(w, "  OCT_PUSH_MAX_PAYLOAD_BYTES=5242880")
	fmt.Fprintln(w, "  OCT_LOG_PATH=")
	fmt.Fprintln(w, "  OCT_RETENTION_DAYS=3")
	fmt.Fprintln(w, "  OCT_MAX_TEXT_BYTES=16384")
	fmt.Fprintln(w, "  OCT_METRICS_INTERVAL=15s")
	fmt.Fprintln(w, "  OCT_CLEANUP_INTERVAL=5m")
	fmt.Fprintln(w, "  OCT_WAL_CHECKPOINT_INTERVAL=10m")
	fmt.Fprintln(w, "  OCT_WAL_RESTART_THRESHOLD_BYTES=52428800")
	fmt.Fprintln(w, "  OCT_CLEANUP_DISK_THRESHOLD=80")
	fmt.Fprintln(w, "  OCT_CLEANUP_DB_THRESHOLD_BYTES=104857600")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --help")
	fmt.Fprintln(w, "  --version")
}
