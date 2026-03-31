package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/bus"
	"github.com/wolandomny/retinue/internal/telegram"
	"github.com/wolandomny/retinue/internal/workspace"
)

func newTelegramCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Telegram integration",
	}
	cmd.AddCommand(newTelegramSetupCmd())
	cmd.AddCommand(newTelegramBridgeCmd())
	return cmd
}

func newTelegramSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup for Telegram bot integration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTelegramSetup(cmd.Context())
		},
	}
}

func runTelegramSetup(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	// Step 1: Bot creation instructions and token input.
	fmt.Println("Telegram Bot Setup")
	fmt.Println("==================")
	fmt.Println()
	fmt.Println("Step 1: Create a Telegram bot")
	fmt.Println()
	fmt.Println("  1. Open Telegram and search for @BotFather")
	fmt.Println("  2. Send /newbot")
	fmt.Println(`  3. Choose a name (e.g., "My Retinue Bot")`)
	fmt.Println(`  4. Choose a username (e.g., "my_retinue_bot")`)
	fmt.Println("  5. BotFather will give you a token like: 123456:ABC-DEF...")
	fmt.Println()

	var token string
	var bot *telegram.Bot
	var botUser *telegram.User

	for {
		fmt.Print("Paste your bot token: ")
		if !scanner.Scan() {
			return fmt.Errorf("failed to read input")
		}
		token = strings.TrimSpace(scanner.Text())
		if token == "" {
			fmt.Println("Token cannot be empty. Please try again.")
			continue
		}

		bot = telegram.New(token)
		var err error
		botUser, err = bot.GetMe(ctx)
		if err != nil {
			fmt.Printf("Invalid token: %v\n", err)
			fmt.Println("Please try again.")
			continue
		}
		fmt.Printf("Bot verified: @%s\n", botUser.Username)
		break
	}

	// Step 2: Get chat ID.
	fmt.Println()
	fmt.Println("Step 2: Connect your Telegram account")
	fmt.Println()
	fmt.Printf("  1. Open Telegram and find your new bot (search for @%s)\n", botUser.Username)
	fmt.Println(`  2. Send it any message (e.g., "hello")`)
	fmt.Println("  3. Press Enter here once you've sent the message...")
	fmt.Println()

	if !scanner.Scan() {
		return fmt.Errorf("failed to read input")
	}

	updates, err := bot.GetUpdates(ctx, 0, 5)
	if err != nil {
		return fmt.Errorf("fetching updates: %w", err)
	}

	if len(updates) == 0 {
		return fmt.Errorf("no messages received; please send a message to @%s and try again", botUser.Username)
	}

	// Find the first update with a message to extract the chat ID.
	var chatID int64
	for _, u := range updates {
		if u.Message != nil {
			chatID = u.Message.Chat.ID
			break
		}
	}
	if chatID == 0 {
		return fmt.Errorf("no messages found in updates; please send a text message to @%s and try again", botUser.Username)
	}

	fmt.Printf("Found chat ID: %d\n", chatID)

	// Send confirmation message.
	_, err = bot.SendMessage(ctx, chatID, "Retinue connected! You'll receive messages here from Woland.")
	if err != nil {
		return fmt.Errorf("sending confirmation message: %w", err)
	}
	fmt.Println("Confirmation message sent!")

	// Step 3: Save configuration.
	fmt.Println()

	ws, err := loadWorkspace()
	if err != nil {
		return fmt.Errorf("loading workspace: %w", err)
	}

	ws.Config.Telegram = &workspace.TelegramConfig{
		Token:  token,
		ChatID: chatID,
	}
	if err := ws.SaveConfig(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Println("Token and chat ID saved to retinue.yaml.")

	// Write .mcp.json with the retinue-telegram server config.
	mcpPath := filepath.Join(ws.Path, ".mcp.json")
	mcpCfg := map[string]interface{}{}

	if existing, err := os.ReadFile(mcpPath); err == nil {
		if err := json.Unmarshal(existing, &mcpCfg); err != nil {
			return fmt.Errorf("parsing existing .mcp.json: %w", err)
		}
	}

	servers, ok := mcpCfg["mcpServers"].(map[string]interface{})
	if !ok {
		servers = map[string]interface{}{}
	}
	servers["retinue-telegram"] = map[string]interface{}{
		"command": "retinue",
		"args":    []string{"mcp", "telegram"},
		"env": map[string]string{
			"RETINUE_TELEGRAM_TOKEN":   token,
			"RETINUE_TELEGRAM_CHAT_ID": fmt.Sprintf("%d", chatID),
		},
	}
	mcpCfg["mcpServers"] = servers

	mcpData, err := json.MarshalIndent(mcpCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling .mcp.json: %w", err)
	}
	if err := os.WriteFile(mcpPath, mcpData, 0o644); err != nil {
		return fmt.Errorf("writing .mcp.json: %w", err)
	}
	fmt.Printf("MCP config written to %s\n", mcpPath)

	fmt.Println()
	fmt.Println("Setup complete! Start a Woland session to test:")
	fmt.Println()
	fmt.Println("  retinue woland talk")

	return nil
}

func newTelegramBridgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bridge",
		Short: "Bridge Telegram to the multi-agent message bus",
		Long: `Runs a daemon that bridges a Telegram chat to the retinue message bus,
allowing the user to participate in the group chat from their phone.

Agent messages on the bus are forwarded to Telegram, and Telegram messages
are written to the bus as user messages.

Requires:
  - telegram.token in retinue.yaml or RETINUE_TELEGRAM_TOKEN environment variable
  - telegram.chat_id in retinue.yaml or RETINUE_TELEGRAM_CHAT_ID environment variable`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.New(os.Stderr, "telegram-bridge: ", log.LstdFlags)

			// Load workspace.
			ws, err := loadWorkspace()
			if err != nil {
				return fmt.Errorf("loading workspace: %w", err)
			}

			// Get Telegram token from workspace config or env var.
			var token string
			if ws.Config.Telegram != nil && ws.Config.Telegram.Token != "" {
				token = ws.Config.Telegram.Token
			} else if envToken := os.Getenv("RETINUE_TELEGRAM_TOKEN"); envToken != "" {
				token = envToken
			} else {
				return fmt.Errorf("telegram token is required: set telegram.token in retinue.yaml or RETINUE_TELEGRAM_TOKEN environment variable")
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

			// Create Telegram bot.
			bot := telegram.New(token)

			// Verify bot token.
			me, err := bot.GetMe(cmd.Context())
			if err != nil {
				return fmt.Errorf("verifying telegram token: %w", err)
			}
			logger.Printf("connected as @%s", me.Username)

			// Create the bus.
			b := bus.New(ws.BusPath())

			// Set up context with signal handling.
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			// Create the adapter with cancel func so it can self-terminate on kill-words.
			adapter := bus.NewTelegramAdapter(b, bot, chatID, logger, cancel)

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

			logger.Printf("starting telegram bus bridge (workspace=%s, chat_id=%d)",
				ws.Config.Name, chatID)
			logger.Printf("bus file: %s", ws.BusPath())

			return adapter.Run(ctx)
		},
	}
}
