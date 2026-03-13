package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/phone"
	"github.com/wolandomny/retinue/internal/telegram"
)

func newPhoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "phone",
		Short: "Phone bridge commands",
	}
	cmd.AddCommand(newPhoneServeCmd())
	return cmd
}

func newPhoneServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the Telegram phone bridge daemon for Woland",
		Long: `Runs a persistent daemon that bridges a running Woland Claude Code session
to Telegram, so the user can chat with Woland from their phone.

Woland's assistant messages are forwarded to Telegram, and Telegram messages
are injected into the Woland tmux pane as user input.

Requires:
  - RETINUE_TELEGRAM_TOKEN environment variable (bot token)
  - RETINUE_TELEGRAM_CHAT_ID env var or telegram.chat_id in retinue.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.New(os.Stderr, "phone: ", log.LstdFlags)

			// Load workspace.
			ws, err := loadWorkspace()
			if err != nil {
				return fmt.Errorf("loading workspace: %w", err)
			}

			// Get Telegram token.
			token := os.Getenv("RETINUE_TELEGRAM_TOKEN")
			if token == "" {
				return fmt.Errorf("RETINUE_TELEGRAM_TOKEN environment variable is required")
			}

			// Get chat ID from config or env var.
			var chatID int64
			if ws.Config.Telegram != nil && ws.Config.Telegram.ChatID != 0 {
				chatID = ws.Config.Telegram.ChatID
			}
			if chatIDStr := os.Getenv("RETINUE_TELEGRAM_CHAT_ID"); chatIDStr != "" {
				parsed, err := strconv.ParseInt(chatIDStr, 10, 64)
				if err != nil {
					return fmt.Errorf("RETINUE_TELEGRAM_CHAT_ID must be a numeric chat ID: %w", err)
				}
				chatID = parsed
			}
			if chatID == 0 {
				return fmt.Errorf("telegram chat ID is required: set RETINUE_TELEGRAM_CHAT_ID or configure telegram.chat_id in retinue.yaml")
			}

			// Derive tmux socket name.
			tmuxSocket := "retinue-" + ws.Config.Name

			// Create Telegram bot.
			bot := telegram.New(token)

			// Verify bot token.
			me, err := bot.GetMe(cmd.Context())
			if err != nil {
				return fmt.Errorf("verifying telegram token: %w", err)
			}
			logger.Printf("connected as @%s", me.Username)

			// Create watcher.
			watcher := phone.NewWatcher(ws.Path, logger)

			// Create bridge.
			bridge := phone.NewBridge(bot, chatID, tmuxSocket, watcher, logger)

			// Set up context with signal handling.
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

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

			logger.Printf("starting phone bridge (workspace=%s, chat_id=%d, socket=%s)",
				ws.Config.Name, chatID, tmuxSocket)
			logger.Printf("apartment path: %s", ws.Path)

			return bridge.Run(ctx)
		},
	}
}
