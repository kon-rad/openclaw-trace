package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

var levelVar = new(slog.LevelVar)

func Setup(level string) (*slog.Logger, error) {
	normalized := strings.ToLower(strings.TrimSpace(level))
	if normalized == "" {
		normalized = "info"
	}
	if err := levelVar.UnmarshalText([]byte(normalized)); err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: levelVar,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger, nil
}
