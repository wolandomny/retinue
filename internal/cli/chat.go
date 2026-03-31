package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/bus"
)

// newChatCmd returns a command that allows reading and writing messages on the bus.
func newChatCmd() *cobra.Command {
	var tail bool
	var history int
	var interactive bool

	cmd := &cobra.Command{
		Use:   "chat [message]",
		Short: "Read and write messages on the bus",
		Long: `Chat with agents and view message history.

Usage:
  retinue chat "hello world"         # Send a message
  retinue chat --tail                # Stream messages in real-time
  retinue chat --history 50          # Show last 50 messages
  retinue chat --tail --interactive  # Interactive chat mode`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			busInstance := bus.New(ws.BusPath())

			// Send mode: retinue chat "message"
			if len(args) > 0 {
				if tail || history > 0 || interactive {
					return fmt.Errorf("cannot combine message sending with other modes")
				}

				message := strings.Join(args, " ")
				msg := bus.NewMessage("user", bus.TypeUser, message)

				if err := busInstance.Append(msg); err != nil {
					return fmt.Errorf("failed to send message: %w", err)
				}

				fmt.Fprintln(cmd.OutOrStdout(), bus.FormatMessage(msg))
				return nil
			}

			// History mode: retinue chat --history N
			if history > 0 && !tail {
				messages, err := busInstance.ReadRecent(history)
				if err != nil {
					return fmt.Errorf("failed to read messages: %w", err)
				}

				if len(messages) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No messages yet. Start an agent with `retinue agent start <id>`")
					return nil
				}

				for _, msg := range messages {
					fmt.Fprintln(cmd.OutOrStdout(), bus.FormatMessage(msg))
				}
				return nil
			}

			// Tail mode: retinue chat --tail
			if tail {
				// Show recent messages for context
				recentMessages, err := busInstance.ReadRecent(20)
				if err != nil {
					return fmt.Errorf("failed to read recent messages: %w", err)
				}

				if len(recentMessages) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No messages yet. Start an agent with `retinue agent start <id>`")
					fmt.Fprintln(cmd.OutOrStdout(), "Waiting for new messages...")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "=== Recent messages ===")
					for _, msg := range recentMessages {
						fmt.Fprintln(cmd.OutOrStdout(), bus.FormatMessage(msg))
					}
					fmt.Fprintln(cmd.OutOrStdout(), "=== Live messages ===")
				}

				// Set up context for graceful shutdown. Use the command's context
				// as parent so callers (including tests) can cancel externally.
				ctx, cancel := context.WithCancel(cmd.Context())
				defer cancel()

				// Handle Ctrl+C
				sigChan := make(chan os.Signal, 1)
				signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
				go func() {
					<-sigChan
					cancel()
				}()

				// Interactive mode: read from stdin while tailing
				if interactive {
					// Start a goroutine for reading user input
					go func() {
						scanner := bufio.NewScanner(os.Stdin)
						for scanner.Scan() {
							text := strings.TrimSpace(scanner.Text())
							if text == "" {
								continue
							}

							msg := bus.NewMessage("user", bus.TypeUser, text)
							if err := busInstance.Append(msg); err != nil {
								fmt.Fprintf(cmd.ErrOrStderr(), "Failed to send message: %v\n", err)
							}
						}
					}()
				}

				// Tail messages
				msgChan := busInstance.Tail(ctx)
				for {
					select {
					case msg, ok := <-msgChan:
						if !ok {
							return nil
						}
						fmt.Fprintln(cmd.OutOrStdout(), bus.FormatMessage(msg))
					case <-ctx.Done():
						return nil
					}
				}
			}

			// Default: show help
			return cmd.Help()
		},
	}

	cmd.Flags().BoolVar(&tail, "tail", false, "stream messages in real-time")
	cmd.Flags().IntVar(&history, "history", 0, "show last N messages (default 50 if no number given)")
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "interactive chat mode (use with --tail)")

	return cmd
}