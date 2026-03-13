package phone

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/wolandomny/retinue/internal/telegram"
)

// Bridge connects a Telegram bot to a Woland Claude Code session via a
// session file watcher and tmux send-keys.
type Bridge struct {
	bot        *telegram.Bot
	chatID     int64
	tmuxSocket string
	watcher    *Watcher
	logger     *log.Logger
}

// NewBridge creates a new Bridge.
func NewBridge(bot *telegram.Bot, chatID int64, tmuxSocket string, watcher *Watcher, logger *log.Logger) *Bridge {
	return &Bridge{
		bot:        bot,
		chatID:     chatID,
		tmuxSocket: tmuxSocket,
		watcher:    watcher,
		logger:     logger,
	}
}

// Run starts the bridge and blocks until the context is cancelled.
func (b *Bridge) Run(ctx context.Context) error {
	// Send startup message.
	if _, err := b.bot.SendMessage(ctx, b.chatID, "📱 Phone bridge active. Watching Woland session."); err != nil {
		b.logger.Printf("warning: failed to send startup message: %v", err)
	}

	// Drain stale Telegram updates.
	offset, err := b.drainUpdates(ctx)
	if err != nil {
		return fmt.Errorf("draining pending updates: %w", err)
	}
	b.logger.Printf("drained pending telegram updates, offset=%d", offset)

	// Start session watcher.
	sessionSwitch := make(chan struct{}, 1)
	watchCh := b.watcher.Watch(ctx, sessionSwitch)

	// Error channel for the telegram listener goroutine.
	errCh := make(chan error, 1)

	// Start Telegram listener.
	telegramCh := make(chan string, 16)
	go func() {
		if err := b.listenTelegram(ctx, &offset, telegramCh); err != nil {
			errCh <- err
		}
	}()

	// Main event loop.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case text, ok := <-watchCh:
			if !ok {
				return fmt.Errorf("session watcher channel closed")
			}
			if _, err := b.bot.SendMessage(ctx, b.chatID, text); err != nil {
				b.logger.Printf("error sending to telegram: %v", err)
			}

		case <-sessionSwitch:
			if _, err := b.bot.SendMessage(ctx, b.chatID, "🔄 New Woland session detected, switching."); err != nil {
				b.logger.Printf("error sending session switch notification: %v", err)
			}

		case msg := <-telegramCh:
			if err := b.sendToTmux(ctx, msg); err != nil {
				b.logger.Printf("error sending to tmux: %v", err)
			}

		case err := <-errCh:
			return fmt.Errorf("telegram listener error: %w", err)
		}
	}
}

// drainUpdates consumes all pending Telegram updates and returns the next
// offset to use.
func (b *Bridge) drainUpdates(ctx context.Context) (int64, error) {
	updates, err := b.bot.GetUpdates(ctx, -1, 0)
	if err != nil {
		return 0, fmt.Errorf("fetching latest update: %w", err)
	}

	if len(updates) == 0 {
		return 0, nil
	}

	offset := updates[len(updates)-1].UpdateID + 1
	b.logger.Printf("drained %d pending update(s)", len(updates))
	return offset, nil
}

// listenTelegram polls Telegram for new messages and sends them to the output channel.
func (b *Bridge) listenTelegram(ctx context.Context, offset *int64, out chan<- string) error {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		updates, err := b.bot.GetUpdates(ctx, *offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			b.logger.Printf("telegram poll error (backoff %v): %v", backoff, err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			// Exponential backoff.
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// Reset backoff on success.
		backoff = time.Second

		for _, update := range updates {
			if update.UpdateID >= *offset {
				*offset = update.UpdateID + 1
			}

			if update.Message == nil || update.Message.Text == "" {
				continue
			}

			// Skip bot messages.
			if update.Message.From != nil && update.Message.From.IsBot {
				continue
			}

			// Skip messages from other chats.
			if update.Message.Chat.ID != b.chatID {
				continue
			}

			select {
			case out <- update.Message.Text:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// sendToTmux injects a message into the Woland tmux pane via send-keys.
func (b *Bridge) sendToTmux(ctx context.Context, message string) error {
	escaped := EscapeTmux(message)
	target := "retinue:woland"

	args := []string{}
	if b.tmuxSocket != "" {
		args = append(args, "-L", b.tmuxSocket)
	}
	args = append(args, "send-keys", "-t", target, "--", escaped, "Enter")

	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, string(out))
	}
	b.logger.Printf("sent to tmux: %q", message)
	return nil
}

// EscapeTmux escapes a message string for use with tmux send-keys.
// It handles special characters that tmux might interpret.
func EscapeTmux(s string) string {
	// Replace newlines with spaces — send-keys treats Enter as a key literal,
	// so we collapse multi-line input into a single line.
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")

	// Escape backslashes first (before adding more).
	s = strings.ReplaceAll(s, `\`, `\\`)

	// Escape semicolons — tmux uses ; as a command separator.
	s = strings.ReplaceAll(s, ";", `\;`)

	// Escape dollar signs to prevent shell variable expansion.
	s = strings.ReplaceAll(s, "$", `\$`)

	// Escape backticks.
	s = strings.ReplaceAll(s, "`", "\\`")

	return s
}
