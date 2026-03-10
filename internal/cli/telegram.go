package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/telegram"
	"github.com/wolandomny/retinue/internal/workspace"
)

func newTelegramCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Telegram integration",
	}
	cmd.AddCommand(newTelegramSetupCmd())
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
		ChatID: chatID,
	}
	if err := ws.SaveConfig(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Println("Chat ID saved to retinue.yaml.")

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

	// Step 3: Shell profile configuration.
	fmt.Println()
	fmt.Println("Step 3: Configure your environment")
	fmt.Println()
	fmt.Println("  Add this to your shell profile (~/.zshrc or ~/.bashrc):")
	fmt.Println()
	fmt.Printf("    export RETINUE_TELEGRAM_TOKEN=\"%s\"\n", token)
	fmt.Println()
	fmt.Println("  Then reload: source ~/.zshrc")

	fmt.Println()
	fmt.Println("Setup complete! Start a Woland session to test:")
	fmt.Println()
	fmt.Println("  retinue woland talk")

	return nil
}
