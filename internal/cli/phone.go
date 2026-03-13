package cli

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/template"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/phone"
	"github.com/wolandomny/retinue/internal/telegram"
)

const (
	launchdLabel  = "com.retinue.phone"
	plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.BinaryPath}}</string>
		<string>phone</string>
		<string>serve</string>
		<string>--workspace</string>
		<string>{{.WorkspacePath}}</string>
	</array>
	<key>WorkingDirectory</key>
	<string>{{.WorkspacePath}}</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>RETINUE_TELEGRAM_TOKEN</key>
		<string>{{.TelegramToken}}</string>
		<key>RETINUE_TELEGRAM_CHAT_ID</key>
		<string>{{.TelegramChatID}}</string>
	</dict>
	<key>KeepAlive</key>
	<true/>
	<key>RunAtLoad</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.ErrorLogPath}}</string>
</dict>
</plist>`
)

type plistData struct {
	Label          string
	BinaryPath     string
	WorkspacePath  string
	TelegramToken  string
	TelegramChatID string
	LogPath        string
	ErrorLogPath   string
}

func newPhoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "phone",
		Short: "Telegram phone daemon for Woland sessions",
		Long:  "Manage the Retinue phone daemon that bridges Woland sessions to Telegram for mobile access.",
	}

	cmd.AddCommand(
		newPhoneServeCmd(),
		newPhoneInstallCmd(),
		newPhoneUninstallCmd(),
		newPhoneStartCmd(),
		newPhoneStopCmd(),
		newPhoneStatusCmd(),
		newPhoneLogsCmd(),
	)

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
  - telegram.token in retinue.yaml or RETINUE_TELEGRAM_TOKEN environment variable (bot token)
  - RETINUE_TELEGRAM_CHAT_ID env var or telegram.chat_id in retinue.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.New(os.Stderr, "phone: ", log.LstdFlags)

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

func newPhoneInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the phone daemon as a launchd service",
		Long:  "Generates a launchd plist file and registers it to run the phone daemon automatically.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPhoneInstall()
		},
	}
}

func newPhoneUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the phone daemon launchd service",
		Long:  "Unloads the launchd service and removes the plist file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPhoneUninstall()
		},
	}
}

func newPhoneStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the phone daemon service",
		Long:  "Starts the installed phone daemon using launchctl.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPhoneStart()
		},
	}
}

func newPhoneStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the phone daemon service",
		Long:  "Stops the running phone daemon using launchctl.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPhoneStop()
		},
	}
}

func newPhoneStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show phone daemon service status",
		Long:  "Shows whether the daemon is running and displays log file paths.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPhoneStatus()
		},
	}
}

func newPhoneLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Tail the phone daemon log files",
		Long:  "Follow the daemon's stdout and stderr log files in real-time.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPhoneLogs()
		},
	}
}

func runPhoneInstall() error {
	ws, err := loadWorkspace()
	if err != nil {
		return fmt.Errorf("loading workspace: %w", err)
	}

	// Get Telegram configuration
	var token string
	if ws.Config.Telegram != nil && ws.Config.Telegram.Token != "" {
		token = ws.Config.Telegram.Token
	} else if envToken := os.Getenv("RETINUE_TELEGRAM_TOKEN"); envToken != "" {
		token = envToken
	} else {
		return fmt.Errorf("telegram token is required: set telegram.token in retinue.yaml or RETINUE_TELEGRAM_TOKEN environment variable")
	}

	var chatID string
	if ws.Config.Telegram != nil {
		chatID = fmt.Sprintf("%d", ws.Config.Telegram.ChatID)
	} else {
		chatIDStr := os.Getenv("RETINUE_TELEGRAM_CHAT_ID")
		if chatIDStr == "" {
			return fmt.Errorf("RETINUE_TELEGRAM_CHAT_ID environment variable or workspace telegram config is required")
		}
		chatID = chatIDStr
	}

	// Get binary path
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting executable path: %w", err)
	}

	// Set up log paths
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	logDir := filepath.Join(homeDir, "Library", "Logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	data := plistData{
		Label:          launchdLabel,
		BinaryPath:     binaryPath,
		WorkspacePath:  ws.Path,
		TelegramToken:  token,
		TelegramChatID: chatID,
		LogPath:        filepath.Join(logDir, "retinue-phone.log"),
		ErrorLogPath:   filepath.Join(logDir, "retinue-phone.err.log"),
	}

	// Generate plist content
	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return fmt.Errorf("parsing plist template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("executing plist template: %w", err)
	}

	// Write plist file
	launchAgentsDir := filepath.Join(homeDir, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgentsDir, 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents directory: %w", err)
	}

	plistPath := filepath.Join(launchAgentsDir, launchdLabel+".plist")
	if err := os.WriteFile(plistPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing plist file: %w", err)
	}

	// Load the plist with launchctl
	cmd := exec.Command("launchctl", "load", plistPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Check if it's already loaded
		if strings.Contains(string(out), "already loaded") {
			fmt.Printf("Service already installed at: %s\n", plistPath)
			return nil
		}
		return fmt.Errorf("loading plist with launchctl: %w\nOutput: %s", err, string(out))
	}

	fmt.Printf("Phone daemon installed successfully!\n")
	fmt.Printf("Plist file: %s\n", plistPath)
	fmt.Printf("Log files: %s, %s\n", data.LogPath, data.ErrorLogPath)
	fmt.Printf("The daemon will start automatically on login.\n")

	return nil
}

func runPhoneUninstall() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", launchdLabel+".plist")

	// Unload the service
	cmd := exec.Command("launchctl", "unload", plistPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Check if it's not loaded
		if strings.Contains(string(out), "No such file or directory") ||
			strings.Contains(string(out), "Could not find specified service") {
			fmt.Println("Service not currently loaded")
		} else {
			fmt.Printf("Warning: failed to unload service: %v\nOutput: %s\n", err, string(out))
		}
	} else {
		fmt.Println("Service unloaded")
	}

	// Remove the plist file
	if err := os.Remove(plistPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Plist file not found")
		} else {
			return fmt.Errorf("removing plist file: %w", err)
		}
	} else {
		fmt.Printf("Removed plist file: %s\n", plistPath)
	}

	fmt.Println("Phone daemon uninstalled successfully!")
	return nil
}

func runPhoneStart() error {
	cmd := exec.Command("launchctl", "start", launchdLabel)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("starting service: %w\nOutput: %s", err, string(out))
	}

	fmt.Println("Phone daemon started successfully!")
	return runPhoneStatus()
}

func runPhoneStop() error {
	cmd := exec.Command("launchctl", "stop", launchdLabel)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stopping service: %w\nOutput: %s", err, string(out))
	}

	fmt.Println("Phone daemon stopped successfully!")
	return runPhoneStatus()
}

func runPhoneStatus() error {
	cmd := exec.Command("launchctl", "list", launchdLabel)
	out, err := cmd.CombinedOutput()

	if err != nil {
		if strings.Contains(string(out), "No such process") ||
			strings.Contains(string(out), "Could not find service") {
			fmt.Println("Status: Not running (service not found)")
			fmt.Println("\nTo install the service, run: retinue phone install")
			return nil
		}
		return fmt.Errorf("checking service status: %w\nOutput: %s", err, string(out))
	}

	fmt.Println("Phone daemon status:")
	fmt.Println(string(out))

	// Show log file paths
	homeDir, err := os.UserHomeDir()
	if err == nil {
		logPath := filepath.Join(homeDir, "Library", "Logs", "retinue-phone.log")
		errLogPath := filepath.Join(homeDir, "Library", "Logs", "retinue-phone.err.log")
		fmt.Printf("\nLog files:\n")
		fmt.Printf("  stdout: %s\n", logPath)
		fmt.Printf("  stderr: %s\n", errLogPath)
	}

	return nil
}

func runPhoneLogs() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	logPath := filepath.Join(homeDir, "Library", "Logs", "retinue-phone.log")
	errLogPath := filepath.Join(homeDir, "Library", "Logs", "retinue-phone.err.log")

	// Check if log files exist
	var files []string
	if _, err := os.Stat(logPath); err == nil {
		files = append(files, logPath)
	}
	if _, err := os.Stat(errLogPath); err == nil {
		files = append(files, errLogPath)
	}

	if len(files) == 0 {
		fmt.Printf("No log files found at:\n  %s\n  %s\n", logPath, errLogPath)
		fmt.Println("\nStart the phone daemon to create log files.")
		return nil
	}

	fmt.Println("Following phone daemon logs (Ctrl+C to stop)...")
	fmt.Println("=" + strings.Repeat("=", 50))

	// Use tail -f to follow existing files
	tailCmd := exec.Command("tail", "-f")
	tailCmd.Args = append(tailCmd.Args, files...)
	tailCmd.Stdout = os.Stdout
	tailCmd.Stderr = os.Stderr

	// Set up signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start the tail command
	if err := tailCmd.Start(); err != nil {
		return fmt.Errorf("starting tail command: %w", err)
	}

	// Wait for either the command to finish or signal
	done := make(chan error, 1)
	go func() {
		done <- tailCmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// Signal received, kill the tail command
		if tailCmd.Process != nil {
			tailCmd.Process.Kill()
		}
		return nil
	case err := <-done:
		// Command finished
		if err != nil {
			return fmt.Errorf("tail command failed: %w", err)
		}
		return nil
	}
}
