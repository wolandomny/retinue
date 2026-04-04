package bus

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/wolandomny/retinue/internal/telegram"
)

// TelegramAdapter bridges a Telegram chat to the message bus, allowing
// the user to participate in the group chat from their phone.
type TelegramAdapter struct {
	bus        *Bus
	bot        *telegram.Bot
	chatID     int64
	logger     *log.Logger
	cancelFunc context.CancelFunc
}

// NewTelegramAdapter creates a TelegramAdapter that bridges messages between
// the bus and a Telegram chat.
func NewTelegramAdapter(b *Bus, bot *telegram.Bot, chatID int64, logger *log.Logger, cancel context.CancelFunc) *TelegramAdapter {
	return &TelegramAdapter{
		bus:        b,
		bot:        bot,
		chatID:     chatID,
		logger:     logger,
		cancelFunc: cancel,
	}
}

// telegramKillWords are messages that cause the adapter to self-terminate.
// These are consistent with the phone bridge kill words.
var telegramKillWords = []string{
	"back",
	"/desk",
	"at my desk",
	"i'm back",
	"im back",
}

// IsTelegramKillWord returns true if the message matches a kill-word (case-insensitive).
func IsTelegramKillWord(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	for _, kw := range telegramKillWords {
		if lower == kw {
			return true
		}
	}
	return false
}

// FormatForTelegram formats a bus message for display in Telegram.
//
// Formatting rules:
//   - Chat messages:   **Name**: text
//   - Action messages: **Name** [action]: text
//   - Result messages: **Name** [result]: text
//   - System messages: _text_
//   - User messages:   (skipped — don't echo back)
func FormatForTelegram(msg Message) string {
	name := capitalize(msg.Name)

	switch msg.Type {
	case TypeSystem:
		return fmt.Sprintf("_%s_", msg.Text)
	case TypeAction:
		return fmt.Sprintf("**%s** [action]: %s", name, msg.Text)
	case TypeResult:
		return fmt.Sprintf("**%s** [result]: %s", name, msg.Text)
	default: // TypeChat and anything else
		return fmt.Sprintf("**%s**: %s", name, msg.Text)
	}
}

// Run starts the adapter and blocks until the context is cancelled.
func (t *TelegramAdapter) Run(ctx context.Context) error {
	// Send startup message.
	if _, err := t.bot.SendMessage(ctx, t.chatID, "Group chat bridge active."); err != nil {
		t.logger.Printf("warning: failed to send startup message: %v", err)
	}

	// Drain stale Telegram updates.
	offset, err := t.drainUpdates(ctx)
	if err != nil {
		return fmt.Errorf("draining pending updates: %w", err)
	}
	t.logger.Printf("drained pending telegram updates, offset=%d", offset)

	// Start tailing the bus.
	busCh := t.bus.Tail(ctx)

	// Error channel for the telegram listener goroutine.
	errCh := make(chan error, 1)

	// Start Telegram listener.
	telegramCh := make(chan string, 16)
	go func() {
		if err := t.listenTelegram(ctx, &offset, telegramCh); err != nil {
			errCh <- err
		}
	}()

	// Main event loop.
	for {
		select {
		case <-ctx.Done():
			return nil

		case msg, ok := <-busCh:
			if !ok {
				return fmt.Errorf("bus tail channel closed")
			}
			// Don't echo the user's own messages back.
			if msg.Type == TypeUser {
				continue
			}
			formatted := FormatForTelegram(*msg)
			if _, err := t.bot.SendMessage(ctx, t.chatID, formatted); err != nil {
				t.logger.Printf("error sending to telegram: %v", err)
			} else {
				preview := formatted
				if len(preview) > 50 {
					preview = preview[:50] + "..."
				}
				t.logger.Printf("sent to telegram: %q", preview)
			}

		case text := <-telegramCh:
			if IsTelegramKillWord(text) {
				t.logger.Printf("kill-word received: %q, shutting down", text)
				// Write system message to bus.
				if err := t.bus.Append(NewMessage("system", TypeSystem, "User has left")); err != nil {
					t.logger.Printf("error writing leave message to bus: %v", err)
				}
				// Send confirmation to Telegram.
				if _, err := t.bot.SendMessage(ctx, t.chatID, "Group chat bridge closing. Welcome back!"); err != nil {
					t.logger.Printf("error sending shutdown confirmation: %v", err)
				}
				// Cancel the context to trigger graceful shutdown.
				if t.cancelFunc != nil {
					t.cancelFunc()
				}
				return nil
			}
			// Write user message to bus.
			msg := NewMessage("user", TypeUser, text)
			msg.To = []string{"woland"}
			if err := t.bus.Append(msg); err != nil {
				t.logger.Printf("error writing user message to bus: %v", err)
			}

		case err := <-errCh:
			return fmt.Errorf("telegram listener error: %w", err)
		}
	}
}

// drainUpdates consumes all pending Telegram updates and returns the next
// offset to use. This prevents stale messages from being processed on startup.
func (t *TelegramAdapter) drainUpdates(ctx context.Context) (int64, error) {
	updates, err := t.bot.GetUpdates(ctx, -1, 0)
	if err != nil {
		return 0, fmt.Errorf("fetching latest update: %w", err)
	}

	if len(updates) == 0 {
		return 0, nil
	}

	offset := updates[len(updates)-1].UpdateID + 1
	t.logger.Printf("drained %d pending update(s)", len(updates))
	return offset, nil
}

// is409Conflict returns true if the error indicates a 409 conflict from
// Telegram (another client polling the same bot).
func is409Conflict(err error) bool {
	errStr := err.Error()
	if strings.Contains(errStr, "unexpected HTTP status 409") {
		return true
	}
	if strings.Contains(errStr, "terminated by other getUpdates request") {
		return true
	}
	return false
}

// listenTelegram polls Telegram for new messages and sends them to the output
// channel. It implements exponential backoff on errors and conflict detection.
func (t *TelegramAdapter) listenTelegram(ctx context.Context, offset *int64, out chan<- string) error {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		updates, err := t.bot.GetUpdates(ctx, *offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			// Check for 409 conflict (another client polling).
			if is409Conflict(err) {
				t.logger.Printf("another Telegram client is polling this bot — adapter may miss messages (backoff %v): %v", backoff, err)
				conflictBackoff := 5 * time.Second
				if backoff > conflictBackoff {
					conflictBackoff = backoff
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(conflictBackoff):
				}
			} else {
				t.logger.Printf("telegram poll error (backoff %v): %v", backoff, err)
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(backoff):
				}
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
			if update.Message.Chat.ID != t.chatID {
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
