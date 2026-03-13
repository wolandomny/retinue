package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestBot creates a Bot pointed at the given test server.
func newTestBot(t *testing.T, server *httptest.Server) *Bot {
	t.Helper()
	bot := New("test-token")
	bot.baseURL = server.URL
	return bot
}

func TestGetMe_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/getMe") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		resp := APIResponse[User]{
			OK: true,
			Result: User{
				ID:        123,
				IsBot:     true,
				FirstName: "TestBot",
				Username:  "test_bot",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	bot := newTestBot(t, server)
	user, err := bot.GetMe(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if user.ID != 123 {
		t.Errorf("expected user ID 123, got %d", user.ID)
	}
	if !user.IsBot {
		t.Error("expected IsBot to be true")
	}
	if user.Username != "test_bot" {
		t.Errorf("expected username test_bot, got %s", user.Username)
	}
}

func TestGetMe_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := APIResponse[User]{
			OK:          false,
			Description: "Unauthorized",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	bot := newTestBot(t, server)
	_, err := bot.GetMe(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Unauthorized") {
		t.Errorf("expected error to contain 'Unauthorized', got: %v", err)
	}
}

func TestSendMessage_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body sendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}

		if body.ChatID != 42 {
			t.Errorf("expected chat_id 42, got %d", body.ChatID)
		}
		if body.Text != "Hello, world!" {
			t.Errorf("expected text 'Hello, world!', got %q", body.Text)
		}
		if body.ParseMode != "Markdown" {
			t.Errorf("expected parse_mode Markdown, got %s", body.ParseMode)
		}

		resp := APIResponse[Message]{
			OK: true,
			Result: Message{
				ID:   1,
				Chat: Chat{ID: 42, Type: "private"},
				Text: body.Text,
				Date: 1700000000,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	bot := newTestBot(t, server)
	msg, err := bot.SendMessage(context.Background(), 42, "Hello, world!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.ID != 1 {
		t.Errorf("expected message ID 1, got %d", msg.ID)
	}
	if msg.Chat.ID != 42 {
		t.Errorf("expected chat ID 42, got %d", msg.Chat.ID)
	}
	if msg.Text != "Hello, world!" {
		t.Errorf("expected text 'Hello, world!', got %q", msg.Text)
	}
}

func TestSendMessage_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := APIResponse[Message]{
			OK:          false,
			Description: "Bad Request: chat not found",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	bot := newTestBot(t, server)
	_, err := bot.SendMessage(context.Background(), 42, "test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("expected error to contain 'chat not found', got: %v", err)
	}
}

func TestSendMessage_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	bot := newTestBot(t, server)
	_, err := bot.SendMessage(context.Background(), 42, "test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain status code 500, got: %v", err)
	}
}

func TestSendMessage_Chunking(t *testing.T) {
	var receivedTexts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body sendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		receivedTexts = append(receivedTexts, body.Text)

		resp := APIResponse[Message]{
			OK: true,
			Result: Message{
				ID:   int64(len(receivedTexts)),
				Chat: Chat{ID: 42, Type: "private"},
				Text: body.Text,
				Date: 1700000000,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	bot := newTestBot(t, server)

	// Create a message longer than maxMessageLength.
	longText := strings.Repeat("a", maxMessageLength+100)
	msg, err := bot.SendMessage(context.Background(), 42, longText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(receivedTexts) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(receivedTexts))
	}

	// Verify all text was sent.
	totalLen := 0
	for _, chunk := range receivedTexts {
		totalLen += len(chunk)
		if len(chunk) > maxMessageLength {
			t.Errorf("chunk exceeds max length: %d > %d", len(chunk), maxMessageLength)
		}
	}
	if totalLen != len(longText) {
		t.Errorf("total chunk length %d does not match original %d", totalLen, len(longText))
	}

	// The returned message should be from the last chunk.
	if msg.ID != 2 {
		t.Errorf("expected last message ID 2, got %d", msg.ID)
	}
}

func TestGetUpdates_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body getUpdatesRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}

		if body.Offset != 100 {
			t.Errorf("expected offset 100, got %d", body.Offset)
		}
		if body.Timeout != 30 {
			t.Errorf("expected timeout 30, got %d", body.Timeout)
		}

		resp := APIResponse[[]Update]{
			OK: true,
			Result: []Update{
				{
					UpdateID: 100,
					Message: &Message{
						ID:   10,
						Chat: Chat{ID: 1, Type: "private"},
						Text: "first",
						Date: 1700000001,
					},
				},
				{
					UpdateID: 101,
					Message: &Message{
						ID:   11,
						Chat: Chat{ID: 1, Type: "private"},
						Text: "second",
						Date: 1700000002,
					},
				},
				{
					UpdateID: 102,
					Message: &Message{
						ID:   12,
						Chat: Chat{ID: 2, Type: "group"},
						Text: "third",
						Date: 1700000003,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	bot := newTestBot(t, server)
	updates, err := bot.GetUpdates(context.Background(), 100, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(updates) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(updates))
	}

	if updates[0].UpdateID != 100 {
		t.Errorf("expected first update ID 100, got %d", updates[0].UpdateID)
	}
	if updates[0].Message.Text != "first" {
		t.Errorf("expected first message text 'first', got %q", updates[0].Message.Text)
	}
	if updates[2].Message.Chat.ID != 2 {
		t.Errorf("expected third message chat ID 2, got %d", updates[2].Message.Chat.ID)
	}
}

func TestGetUpdates_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := APIResponse[[]Update]{
			OK:     true,
			Result: []Update{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	bot := newTestBot(t, server)
	updates, err := bot.GetUpdates(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(updates) != 0 {
		t.Errorf("expected 0 updates, got %d", len(updates))
	}
}

func TestGetUpdates_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := APIResponse[[]Update]{
			OK:          false,
			Description: "Conflict: terminated by other getUpdates request",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	bot := newTestBot(t, server)
	_, err := bot.GetUpdates(context.Background(), 0, 30)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "terminated by other") {
		t.Errorf("expected error to contain 'terminated by other', got: %v", err)
	}
}

func TestContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// This handler should never complete because the context is cancelled.
		select {}
	}))
	defer server.Close()

	bot := newTestBot(t, server)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := bot.GetMe(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}

	_, err = bot.SendMessage(ctx, 42, "test")
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}

	_, err = bot.GetUpdates(ctx, 0, 30)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		maxLen    int
		wantCount int
	}{
		{
			name:      "short message",
			text:      "hello",
			maxLen:    10,
			wantCount: 1,
		},
		{
			name:      "exact length",
			text:      "hello",
			maxLen:    5,
			wantCount: 1,
		},
		{
			name:      "needs splitting",
			text:      strings.Repeat("a", 100),
			maxLen:    40,
			wantCount: 3,
		},
		{
			name:      "split on newline",
			text:      strings.Repeat("a", 30) + "\n" + strings.Repeat("b", 30),
			maxLen:    40,
			wantCount: 2,
		},
		{
			name:      "empty message",
			text:      "",
			maxLen:    10,
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := splitMessage(tt.text, tt.maxLen)
			if len(chunks) != tt.wantCount {
				t.Errorf("expected %d chunks, got %d", tt.wantCount, len(chunks))
			}

			// Verify all chunks are within the max length.
			for i, chunk := range chunks {
				if len(chunk) > tt.maxLen {
					t.Errorf("chunk %d exceeds max length: %d > %d", i, len(chunk), tt.maxLen)
				}
			}

			// Verify the concatenation matches the original text.
			joined := strings.Join(chunks, "")
			if joined != tt.text {
				t.Errorf("joined chunks do not match original text")
			}
		})
	}
}

func TestSendMessage_MarkdownFallback(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		var body sendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}

		if callCount == 1 {
			// First call: Telegram returns HTTP 400 with Markdown parse error,
			// which is what happens in production.
			if body.ParseMode != "Markdown" {
				t.Errorf("expected first call to have parse_mode Markdown, got %s", body.ParseMode)
			}

			resp := APIResponse[Message]{
				OK:          false,
				Description: "Bad Request: can't parse entities: Can't find end of the entity starting at byte offset 42",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(resp)
		} else if callCount == 2 {
			// Second call: succeed without parse mode
			if body.ParseMode != "" {
				t.Errorf("expected second call to have empty parse_mode, got %s", body.ParseMode)
			}

			resp := APIResponse[Message]{
				OK: true,
				Result: Message{
					ID:   1,
					Chat: Chat{ID: 42, Type: "private"},
					Text: body.Text,
					Date: 1700000000,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		} else {
			t.Errorf("unexpected third call to sendMessage")
		}
	}))
	defer server.Close()

	bot := newTestBot(t, server)
	msg, err := bot.SendMessage(context.Background(), 42, "message with *bad* markdown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 API calls, got %d", callCount)
	}

	if msg.ID != 1 {
		t.Errorf("expected message ID 1, got %d", msg.ID)
	}
	if msg.Text != "message with *bad* markdown" {
		t.Errorf("expected text 'message with *bad* markdown', got %q", msg.Text)
	}
}

func TestSendMessage_MarkdownFallback_NonParseError(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		// Fail with a different error (not a parse error) at HTTP 400.
		resp := APIResponse[Message]{
			OK:          false,
			Description: "Bad Request: chat not found",
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	bot := newTestBot(t, server)
	_, err := bot.SendMessage(context.Background(), 42, "test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Should not attempt fallback for non-parse errors.
	if callCount != 1 {
		t.Errorf("expected 1 API call (no retry), got %d", callCount)
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("expected error to contain 'chat not found', got: %v", err)
	}
}

func TestIsMarkdownParseError(t *testing.T) {
	tests := []struct {
		description string
		want        bool
	}{
		{
			description: "Bad Request: can't parse entities: Can't find end of the entity starting at byte offset 42",
			want:        true,
		},
		{
			description: "Bad Request: can't parse entities",
			want:        true,
		},
		{
			description: "Bad Request: chat not found",
			want:        false,
		},
		{
			description: "Forbidden: bot was blocked by the user",
			want:        false,
		},
		{
			description: "",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			got := isMarkdownParseError(tt.description)
			if got != tt.want {
				t.Errorf("isMarkdownParseError(%q) = %v, want %v", tt.description, got, tt.want)
			}
		})
	}
}

func TestEndpoint(t *testing.T) {
	bot := New("my-token")
	got := bot.endpoint("sendMessage")
	want := "https://api.telegram.org/botmy-token/sendMessage"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}
