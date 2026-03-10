package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	// defaultBaseURL is the base URL for the Telegram Bot API.
	defaultBaseURL = "https://api.telegram.org"

	// maxMessageLength is the maximum length of a single Telegram message.
	maxMessageLength = 4096
)

// Bot is a client for the Telegram Bot API.
type Bot struct {
	token   string
	client  *http.Client
	baseURL string
}

// New creates a new Bot client with the given API token.
func New(token string) *Bot {
	return &Bot{
		token:   token,
		client:  &http.Client{},
		baseURL: defaultBaseURL,
	}
}

// GetMe returns information about the bot. It can be used to verify
// that the bot token is valid.
func (b *Bot) GetMe(ctx context.Context) (*User, error) {
	url := b.endpoint("getMe")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating getMe request: %w", err)
	}

	var resp APIResponse[User]
	if err := b.doRequest(req, &resp); err != nil {
		return nil, fmt.Errorf("executing getMe request: %w", err)
	}

	if !resp.OK {
		return nil, fmt.Errorf("telegram API error on getMe: %s", resp.Description)
	}

	return &resp.Result, nil
}

// sendMessageRequest is the JSON body for the sendMessage API call.
type sendMessageRequest struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// SendMessage sends a text message to the specified chat. Messages longer
// than 4096 characters are automatically split into multiple chunks.
// The last sent message is returned.
func (b *Bot) SendMessage(ctx context.Context, chatID int64, text string) (*Message, error) {
	chunks := splitMessage(text, maxMessageLength)

	var lastMsg *Message
	for _, chunk := range chunks {
		msg, err := b.sendSingleMessage(ctx, chatID, chunk)
		if err != nil {
			return nil, err
		}
		lastMsg = msg
	}

	return lastMsg, nil
}

// sendSingleMessage sends a single text message (must be <= maxMessageLength).
func (b *Bot) sendSingleMessage(ctx context.Context, chatID int64, text string) (*Message, error) {
	body := sendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "Markdown",
	}

	var resp APIResponse[Message]
	if err := b.postJSON(ctx, "sendMessage", body, &resp); err != nil {
		return nil, fmt.Errorf("executing sendMessage request: %w", err)
	}

	if !resp.OK {
		return nil, fmt.Errorf("telegram API error on sendMessage: %s", resp.Description)
	}

	return &resp.Result, nil
}

// getUpdatesRequest is the JSON body for the getUpdates API call.
type getUpdatesRequest struct {
	Offset  int64 `json:"offset"`
	Timeout int   `json:"timeout"`
}

// GetUpdates fetches incoming updates using long polling. The offset parameter
// tells Telegram to only return updates with an update_id greater than or equal
// to offset. The timeout parameter specifies the number of seconds Telegram
// will hold the connection open before returning an empty response.
func (b *Bot) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	body := getUpdatesRequest{
		Offset:  offset,
		Timeout: timeout,
	}

	var resp APIResponse[[]Update]
	if err := b.postJSON(ctx, "getUpdates", body, &resp); err != nil {
		return nil, fmt.Errorf("executing getUpdates request: %w", err)
	}

	if !resp.OK {
		return nil, fmt.Errorf("telegram API error on getUpdates: %s", resp.Description)
	}

	return resp.Result, nil
}

// endpoint returns the full API URL for the given method.
func (b *Bot) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", b.baseURL, b.token, method)
}

// postJSON sends a POST request with a JSON body to the given API method
// and decodes the response into dest.
func (b *Bot) postJSON(ctx context.Context, method string, body any, dest any) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling request body: %w", err)
	}

	url := b.endpoint(method)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("creating %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	return b.doRequest(req, dest)
}

// doRequest executes an HTTP request and decodes the JSON response into dest.
func (b *Bot) doRequest(req *http.Request, dest any) error {
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending HTTP request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected HTTP status %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, dest); err != nil {
		return fmt.Errorf("decoding response JSON: %w", err)
	}

	return nil
}

// splitMessage splits text into chunks of at most maxLen characters.
// It tries to split on newline boundaries when possible to produce
// cleaner output.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		// Try to find a newline to split on within the allowed length.
		splitAt := maxLen
		for i := maxLen - 1; i > maxLen/2; i-- {
			if text[i] == '\n' {
				splitAt = i + 1 // Include the newline in the current chunk.
				break
			}
		}

		chunks = append(chunks, text[:splitAt])
		text = text[splitAt:]
	}

	return chunks
}
