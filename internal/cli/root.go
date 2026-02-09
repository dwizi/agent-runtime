package cli

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/carlos/spinner/internal/app"
	"github.com/carlos/spinner/internal/config"
	"github.com/carlos/spinner/internal/tui"
)

const version = "0.1.0"

func NewRoot(logger *slog.Logger) *cobra.Command {
	root := &cobra.Command{
		Use:   "spinner",
		Short: "Spinner is a channel-first orchestration runtime",
	}

	root.AddCommand(newServeCommand(logger))
	root.AddCommand(newTUICommand(logger))
	root.AddCommand(newVersionCommand())

	return root
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
