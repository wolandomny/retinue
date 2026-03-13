package phone

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wolandomny/retinue/internal/telegram"
)

func TestIsKillWord(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"back", true},
		{"Back", true},
		{"BACK", true},
		{"/desk", true},
		{"/Desk", true},
		{"at my desk", true},
		{"At My Desk", true},
		{"i'm back", true},
		{"I'm Back", true},
		{"im back", true},
		{"Im Back", true},
		{"  back  ", true},  // whitespace trimmed
		{"back!", false},     // not exact match
		{"I'm back!", false}, // not exact match
		{"going back", false},
		{"hello", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isKillWord(tt.input)
			if got != tt.want {
				t.Errorf("isKillWord(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestEscapeTmux(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "semicolons",
			input: "foo; bar; baz",
			want:  `foo\; bar\; baz`,
		},
		{
			name:  "dollar signs",
			input: "echo $HOME $USER",
			want:  `echo \$HOME \$USER`,
		},
		{
			name:  "backticks",
			input: "run `command`",
			want:  "run \\`command\\`",
		},
		{
			name:  "backslashes",
			input: `path\to\file`,
			want:  `path\\to\\file`,
		},
		{
			name:  "newlines collapsed",
			input: "line1\nline2\nline3",
			want:  "line1 line2 line3",
		},
		{
			name:  "carriage returns removed",
			input: "line1\r\nline2",
			want:  "line1 line2",
		},
		{
			name:  "combined special chars",
			input: "echo $HOME; ls `pwd`",
			want:  `echo \$HOME\; ls \` + "`" + `pwd\` + "`",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EscapeTmux(tt.input)
			if got != tt.want {
				t.Errorf("EscapeTmux(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Test helpers for Bridge tests ---

// telegramAPIResponse builds a JSON response matching Telegram's API format.
func telegramAPIResponse(t *testing.T, ok bool, result any, description string) []byte {
	t.Helper()
	resp := map[string]any{
		"ok":     ok,
		"result": result,
	}
	if description != "" {
		resp["description"] = description
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshaling API response: %v", err)
	}
	return data
}

// sentMessage records a sendMessage request received by the test server.
type sentMessage struct {
	ChatID int64  `json:"chat_id"`
	Text   string `json:"text"`
}

// bridgeTestServer creates an httptest.Server that handles sendMessage and
// getUpdates. It records sent messages and returns updates from the provided
// function. getUpdatesFn is called on each getUpdates request; it should
// return a slice of updates (or nil/empty for no updates).
func bridgeTestServer(t *testing.T, sent *[]sentMessage, mu *sync.Mutex, getUpdatesFn func() []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var body struct {
				ChatID int64  `json:"chat_id"`
				Text   string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Logf("sendMessage decode error: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			*sent = append(*sent, sentMessage{ChatID: body.ChatID, Text: body.Text})
			mu.Unlock()

			resp := telegramAPIResponse(t, true, map[string]any{
				"message_id": 1,
				"chat":       map[string]any{"id": body.ChatID},
				"text":       body.Text,
			}, "")
			w.Write(resp)

		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			// Drain body.
			var body json.RawMessage
			json.NewDecoder(r.Body).Decode(&body)

			updates := getUpdatesFn()
			if updates == nil {
				updates = []map[string]any{}
			}
			resp := telegramAPIResponse(t, true, updates, "")
			w.Write(resp)

		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"ok":false,"description":"not found"}`))
		}
	}))
}

// --- Bridge tests ---

func TestIs409Conflict(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "HTTP 409 status",
			err:  fmt.Errorf("unexpected HTTP status 409: conflict"),
			want: true,
		},
		{
			name: "terminated by other getUpdates",
			err:  fmt.Errorf("telegram API error: terminated by other getUpdates request"),
			want: true,
		},
		{
			name: "generic error",
			err:  fmt.Errorf("connection refused"),
			want: false,
		},
		{
			name: "404 status",
			err:  fmt.Errorf("unexpected HTTP status 404: not found"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := is409Conflict(tt.err)
			if got != tt.want {
				t.Errorf("is409Conflict(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestBridge_Run_ForwardsWatcherToTelegram(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sent []sentMessage
	var mu sync.Mutex

	// getUpdates always returns empty (no incoming user messages).
	server := bridgeTestServer(t, &sent, &mu, func() []map[string]any {
		return nil
	})
	defer server.Close()

	bot := telegram.NewWithBaseURL("test-token", server.URL)

	// Set up temp dir as the apartment path with a session file
	// that the Watcher will discover.
	tmpDir := t.TempDir()
	projectDir := ClaudeProjectDir(tmpDir)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("creating project dir: %v", err)
	}

	// Write a session file with Woland keyword in the first 256 bytes.
	sessionFile := filepath.Join(projectDir, "test-session.jsonl")
	lines := strings.Join([]string{
		`{"type":"system","uuid":"sys-1","message":{"content":[{"type":"text","text":"You are Woland, the planning agent."}]}}`,
		`{"type":"assistant","uuid":"test-1","message":{"content":[{"type":"text","text":"Hello from the bridge test"}]}}`,
		`{"type":"assistant","uuid":"test-2","message":{"content":[{"type":"text","text":"Second message from bridge"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionFile, []byte(lines), 0o644); err != nil {
		t.Fatalf("writing session file: %v", err)
	}

	logger := log.New(os.Stderr, "test: ", log.LstdFlags)
	watcher := NewWatcher(tmpDir, logger)

	bridge := NewBridge(bot, 123, "", watcher, logger, cancel)

	// Run the bridge in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		errCh <- bridge.Run(ctx)
	}()

	// Wait for messages to arrive at the test server.
	deadline := time.After(4 * time.Second)
	for {
		mu.Lock()
		count := len(sent)
		mu.Unlock()

		// We expect at least: startup message + 2 assistant messages.
		if count >= 3 {
			break
		}

		select {
		case <-deadline:
			cancel()
			mu.Lock()
			t.Fatalf("timed out waiting for messages, got %d: %+v", len(sent), sent)
			mu.Unlock()
			return
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Cancel and wait for Run to return.
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify that the startup message was sent.
	foundStartup := false
	foundHello := false
	foundSecond := false
	for _, msg := range sent {
		if msg.ChatID != 123 {
			t.Errorf("unexpected chat ID %d, want 123", msg.ChatID)
		}
		if strings.Contains(msg.Text, "Phone bridge active") {
			foundStartup = true
		}
		if strings.Contains(msg.Text, "Hello from the bridge test") {
			foundHello = true
		}
		if strings.Contains(msg.Text, "Second message from bridge") {
			foundSecond = true
		}
	}

	if !foundStartup {
		t.Error("expected startup message to be sent to Telegram")
	}
	if !foundHello {
		t.Error("expected first assistant message to be forwarded to Telegram")
	}
	if !foundSecond {
		t.Error("expected second assistant message to be forwarded to Telegram")
	}
}

func TestBridge_Run_KillWordShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sent []sentMessage
	var mu sync.Mutex

	var callCount atomic.Int32

	// First getUpdates call (for drainUpdates) returns empty.
	// Second call returns a message with kill-word "back".
	// Subsequent calls return empty.
	server := bridgeTestServer(t, &sent, &mu, func() []map[string]any {
		n := callCount.Add(1)
		if n == 2 {
			return []map[string]any{
				{
					"update_id": 100,
					"message": map[string]any{
						"message_id": 10,
						"chat":       map[string]any{"id": 123},
						"from":       map[string]any{"id": 456, "is_bot": false},
						"text":       "back",
					},
				},
			}
		}
		return nil
	})
	defer server.Close()

	bot := telegram.NewWithBaseURL("test-token", server.URL)

	// Create watcher with a temp dir (no session files needed for this test).
	tmpDir := t.TempDir()
	projectDir := ClaudeProjectDir(tmpDir)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("creating project dir: %v", err)
	}

	logger := log.New(os.Stderr, "test: ", log.LstdFlags)
	watcher := NewWatcher(tmpDir, logger)

	var cancelCalled atomic.Bool
	bridgeCancel := func() {
		cancelCalled.Store(true)
		cancel()
	}

	bridge := NewBridge(bot, 123, "nonexistent-socket", watcher, logger, bridgeCancel)

	// Run the bridge — it should return nil on kill-word shutdown.
	errCh := make(chan error, 1)
	go func() {
		errCh <- bridge.Run(ctx)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v, expected nil (graceful shutdown)", err)
		}
	case <-time.After(4 * time.Second):
		cancel()
		t.Fatal("timed out waiting for bridge to shut down on kill-word")
	}

	// Verify cancel was called.
	if !cancelCalled.Load() {
		t.Error("expected cancel function to be called on kill-word")
	}

	// Verify a shutdown confirmation message was sent.
	mu.Lock()
	defer mu.Unlock()
	foundShutdown := false
	for _, msg := range sent {
		if strings.Contains(msg.Text, "Phone bridge closing") {
			foundShutdown = true
			break
		}
	}
	if !foundShutdown {
		t.Error("expected shutdown confirmation message to be sent to Telegram")
	}
}

func TestBridge_DrainUpdates(t *testing.T) {
	tests := []struct {
		name       string
		updates    []map[string]any
		apiError   bool
		wantOffset int64
		wantErr    bool
	}{
		{
			name:       "empty updates",
			updates:    []map[string]any{},
			wantOffset: 0,
			wantErr:    false,
		},
		{
			name: "multiple pending updates",
			updates: []map[string]any{
				{"update_id": 50, "message": map[string]any{"message_id": 1, "chat": map[string]any{"id": 123}, "text": "old1"}},
				{"update_id": 51, "message": map[string]any{"message_id": 2, "chat": map[string]any{"id": 123}, "text": "old2"}},
				{"update_id": 52, "message": map[string]any{"message_id": 3, "chat": map[string]any{"id": 123}, "text": "old3"}},
			},
			wantOffset: 53, // last update_id (52) + 1
			wantErr:    false,
		},
		{
			name:     "API error",
			apiError: true,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tt.apiError {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("internal server error"))
					return
				}
				updates := tt.updates
				if updates == nil {
					updates = []map[string]any{}
				}
				resp := telegramAPIResponse(t, true, updates, "")
				w.Write(resp)
			}))
			defer server.Close()

			bot := telegram.NewWithBaseURL("test-token", server.URL)
			logger := log.New(os.Stderr, "test: ", log.LstdFlags)
			bridge := &Bridge{
				bot:    bot,
				chatID: 123,
				logger: logger,
			}

			offset, err := bridge.drainUpdates(ctx)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if offset != tt.wantOffset {
				t.Errorf("offset = %d, want %d", offset, tt.wantOffset)
			}
		})
	}
}

func TestBridge_ListenTelegram_SkipsBotMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		n := callCount.Add(1)
		if n == 1 {
			// Return a mix of bot and human messages.
			updates := []map[string]any{
				{
					"update_id": 200,
					"message": map[string]any{
						"message_id": 20,
						"chat":       map[string]any{"id": 123},
						"from":       map[string]any{"id": 999, "is_bot": true},
						"text":       "bot message should be skipped",
					},
				},
				{
					"update_id": 201,
					"message": map[string]any{
						"message_id": 21,
						"chat":       map[string]any{"id": 123},
						"from":       map[string]any{"id": 456, "is_bot": false},
						"text":       "human message should pass",
					},
				},
			}
			resp := telegramAPIResponse(t, true, updates, "")
			w.Write(resp)
		} else {
			// Subsequent calls return empty so we don't loop forever.
			resp := telegramAPIResponse(t, true, []map[string]any{}, "")
			w.Write(resp)
		}
	}))
	defer server.Close()

	bot := telegram.NewWithBaseURL("test-token", server.URL)
	logger := log.New(os.Stderr, "test: ", log.LstdFlags)
	bridge := &Bridge{
		bot:    bot,
		chatID: 123,
		logger: logger,
	}

	out := make(chan string, 16)
	var offset int64

	go func() {
		bridge.listenTelegram(ctx, &offset, out)
	}()

	// Wait for the human message.
	select {
	case msg := <-out:
		if msg != "human message should pass" {
			t.Errorf("expected 'human message should pass', got %q", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for message")
	}

	// Verify no bot messages came through by draining the channel briefly.
	select {
	case msg := <-out:
		t.Errorf("unexpected extra message: %q", msg)
	case <-time.After(200 * time.Millisecond):
		// Good — no more messages.
	}

	cancel()
}

func TestBridge_ListenTelegram_SkipsOtherChats(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		n := callCount.Add(1)
		if n == 1 {
			updates := []map[string]any{
				{
					"update_id": 300,
					"message": map[string]any{
						"message_id": 30,
						"chat":       map[string]any{"id": 999}, // wrong chat
						"from":       map[string]any{"id": 456, "is_bot": false},
						"text":       "wrong chat should be skipped",
					},
				},
				{
					"update_id": 301,
					"message": map[string]any{
						"message_id": 31,
						"chat":       map[string]any{"id": 123}, // correct chat
						"from":       map[string]any{"id": 456, "is_bot": false},
						"text":       "correct chat message",
					},
				},
			}
			resp := telegramAPIResponse(t, true, updates, "")
			w.Write(resp)
		} else {
			resp := telegramAPIResponse(t, true, []map[string]any{}, "")
			w.Write(resp)
		}
	}))
	defer server.Close()

	bot := telegram.NewWithBaseURL("test-token", server.URL)
	logger := log.New(os.Stderr, "test: ", log.LstdFlags)
	bridge := &Bridge{
		bot:    bot,
		chatID: 123,
		logger: logger,
	}

	out := make(chan string, 16)
	var offset int64

	go func() {
		bridge.listenTelegram(ctx, &offset, out)
	}()

	// Wait for the correct-chat message.
	select {
	case msg := <-out:
		if msg != "correct chat message" {
			t.Errorf("expected 'correct chat message', got %q", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for message")
	}

	// Verify no wrong-chat messages came through.
	select {
	case msg := <-out:
		t.Errorf("unexpected extra message: %q", msg)
	case <-time.After(200 * time.Millisecond):
		// Good — no more messages.
	}

	cancel()
}
