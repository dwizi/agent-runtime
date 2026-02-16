package main

import (
	"log/slog"
	"os"

	"github.com/dwizi/agent-runtime/internal/cli"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := cli.NewRoot(logger).Execute(); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}
