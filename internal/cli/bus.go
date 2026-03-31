package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/bus"
)

func newBusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bus",
		Short: "Message bus operations",
	}

	cmd.AddCommand(newBusServeCmd())

	return cmd
}

func newBusServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the bus watcher daemon",
		Long:  "Tails the message bus and bridges messages between running agent sessions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			b := bus.New(ws.BusPath())
			tmuxSocket := "retinue-" + ws.Config.Name
			logger := log.New(os.Stderr, "bus: ", log.LstdFlags)

			watcher := bus.NewWatcher(b, tmuxSocket, ws.Path, logger)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Handle SIGINT/SIGTERM for graceful shutdown.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				select {
				case sig := <-sigCh:
					logger.Printf("received signal %v, shutting down", sig)
					cancel()
				case <-ctx.Done():
				}
			}()

			fmt.Fprintln(cmd.OutOrStdout(), "Bus watcher active. Monitoring agent sessions...")

			if err := watcher.Run(ctx); err != nil {
				return fmt.Errorf("bus watcher: %w", err)
			}

			return nil
		},
	}

	return cmd
}
