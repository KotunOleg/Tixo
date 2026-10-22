package logger

import (
	"io"
	"log/slog"
)

func New(w io.Writer, appEnv string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if appEnv != "prod" {
		opts.Level = slog.LevelDebug
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
