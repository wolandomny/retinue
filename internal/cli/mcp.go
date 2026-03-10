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
	"github.com/wolandomny/retinue/internal/mcp"
	"github.com/wolandomny/retinue/internal/telegram"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server commands",
	}
	cmd.AddCommand(newMCPTelegramCmd())
	return cmd
}

func newMCPTelegramCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "telegram",
		Short: "Run Telegram MCP server for Claude Code",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.New(os.Stderr, "mcp-telegram: ", log.LstdFlags)

			token := os.Getenv("RETINUE_TELEGRAM_TOKEN")
			if token == "" {
				return fmt.Errorf("RETINUE_TELEGRAM_TOKEN environment variable is required")
			}

			chatIDStr := os.Getenv("RETINUE_TELEGRAM_CHAT_ID")
			if chatIDStr == "" {
				return fmt.Errorf("RETINUE_TELEGRAM_CHAT_ID environment variable is required")
			}

			chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
			if err != nil {
				return fmt.Errorf("RETINUE_TELEGRAM_CHAT_ID must be a numeric chat ID: %w", err)
			}

			bot := telegram.New(token)

			// Set up context with signal handling for graceful shutdown.
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

			// Drain any pending Telegram updates so we don't process stale messages.
			offset, err := drainUpdates(ctx, bot, logger)
			if err != nil {
				return fmt.Errorf("draining pending updates: %w", err)
			}

			srv := mcp.NewServer("retinue-telegram", "1.0.0")

			srv.AddTool(mcp.ToolDef{
				Name:        "send_telegram",
				Description: "Send a message to the user's Telegram chat. Use this in phone mode to mirror responses when the user is away from the terminal.",
				InputSchema: mcp.InputSchema{
					Type: "object",
					Properties: map[string]mcp.Property{
						"message": {
							Type:        "string",
							Description: "The message text to send",
						},
					},
					Required: []string{"message"},
				},
			}, makeSendHandler(bot, chatID))

			srv.AddTool(mcp.ToolDef{
				Name:        "ask_telegram",
				Description: "Send a question to the user via Telegram and wait for their reply. Use this when the user has indicated they are away from the terminal (e.g., 'stepping away', 'brb', '/phone'). Stop using this when they say 'back', '/desk', or type directly in Claude Code.",
				InputSchema: mcp.InputSchema{
					Type: "object",
					Properties: map[string]mcp.Property{
						"question": {
							Type:        "string",
							Description: "The question to ask the user",
						},
					},
					Required: []string{"question"},
				},
			}, makeAskHandler(bot, chatID, &offset, logger))

			logger.Printf("server starting (chat_id=%d)", chatID)
			return srv.Run(ctx, os.Stdin, os.Stdout)
		},
	}
}

// drainUpdates consumes all pending Telegram updates and returns the next
// offset to use. This prevents the server from processing stale messages
// that arrived before it started.
func drainUpdates(ctx context.Context, bot *telegram.Bot, logger *log.Logger) (int64, error) {
	// Use offset -1 to get only the most recent update, then acknowledge it.
	updates, err := bot.GetUpdates(ctx, -1, 0)
	if err != nil {
		return 0, fmt.Errorf("fetching latest update: %w", err)
	}

	if len(updates) == 0 {
		return 0, nil
	}

	// The next offset is the last update ID + 1.
	offset := updates[len(updates)-1].UpdateID + 1
	logger.Printf("drained %d pending update(s), offset now %d", len(updates), offset)
	return offset, nil
}

// makeSendHandler returns a tool handler that sends a message to Telegram.
func makeSendHandler(bot *telegram.Bot, chatID int64) mcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (string, error) {
		message, ok := args["message"].(string)
		if !ok || message == "" {
			return "", fmt.Errorf("missing or empty 'message' argument")
		}

		if _, err := bot.SendMessage(ctx, chatID, message); err != nil {
			return "", fmt.Errorf("sending telegram message: %w", err)
		}

		return "sent", nil
	}
}

// makeAskHandler returns a tool handler that sends a question to Telegram
// and waits for the user's reply via long polling.
func makeAskHandler(bot *telegram.Bot, chatID int64, offset *int64, logger *log.Logger) mcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (string, error) {
		question, ok := args["question"].(string)
		if !ok || question == "" {
			return "", fmt.Errorf("missing or empty 'question' argument")
		}

		if _, err := bot.SendMessage(ctx, chatID, question); err != nil {
			return "", fmt.Errorf("sending telegram question: %w", err)
		}

		// Long-poll for the user's reply.
		for {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			default:
			}

			updates, err := bot.GetUpdates(ctx, *offset, 30)
			if err != nil {
				// If context was cancelled, return the context error.
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				logger.Printf("error polling for updates: %v", err)
				continue
			}

			for _, update := range updates {
				// Advance offset past this update regardless of whether we use it.
				if update.UpdateID >= *offset {
					*offset = update.UpdateID + 1
				}

				// Skip updates without a text message.
				if update.Message == nil || update.Message.Text == "" {
					continue
				}

				// Skip messages from bots (including our own).
				if update.Message.From != nil && update.Message.From.IsBot {
					continue
				}

				// Skip messages from other chats.
				if update.Message.Chat.ID != chatID {
					continue
				}

				return update.Message.Text, nil
			}
		}
	}
}
