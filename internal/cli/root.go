package cli

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dwizi/agent-runtime/internal/app"
	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/qmd"
	"github.com/dwizi/agent-runtime/internal/tui"
)

const version = "0.1.0"

func NewRoot(logger *slog.Logger) *cobra.Command {
	root := &cobra.Command{
		Use:   "agent-runtime",
		Short: "Agent Runtime is a channel-first orchestration runtime",
	}

	root.AddCommand(newServeCommand(logger))
	root.AddCommand(newQMDSidecarCommand(logger))
	root.AddCommand(newTUICommand(logger))
	root.AddCommand(newChatCommand(logger))
	root.AddCommand(newVersionCommand())

	return root
}

func newQMDSidecarCommand(logger *slog.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "qmd-sidecar",
		Short: "Run qmd sidecar HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.FromEnv()
			sidecarCfg := qmd.Config{
				WorkspaceRoot:   cfg.WorkspaceRoot,
				Binary:          cfg.QMDBinary,
				IndexName:       cfg.QMDIndexName,
				Collection:      cfg.QMDCollectionName,
				SharedModelsDir: cfg.QMDSharedModelsDir,
				SearchLimit:     cfg.QMDSearchLimit,
				OpenMaxBytes:    cfg.QMDOpenMaxBytes,
				Debounce:        time.Duration(cfg.QMDDebounceSeconds) * time.Second,
				IndexTimeout:    time.Duration(cfg.QMDIndexTimeoutSec) * time.Second,
				QueryTimeout:    time.Duration(cfg.QMDQueryTimeoutSec) * time.Second,
				AutoEmbed:       cfg.QMDAutoEmbed,
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return qmd.RunSidecar(ctx, sidecarCfg, cfg.QMDSidecarAddr, logger)
		},
	}
}

func newServeCommand(logger *slog.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run gateway and orchestrator services",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.FromEnv()
			runtime, err := app.New(cfg, logger)
			if err != nil {
				return err
			}
			defer runtime.Close()

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return runtime.Run(ctx)
		},
	}
}

func newTUICommand(logger *slog.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Run the administrative terminal UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.Run(config.FromEnv(), logger)
		},
	}
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print CLI version",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Println(version)
		},
	}
}
