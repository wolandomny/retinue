// Package telegram provides a client for the Telegram Bot API.
// It uses only net/http from the standard library with no external dependencies.
package telegram

// User represents a Telegram user or bot account.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Chat represents a Telegram chat (private, group, supergroup, or channel).
type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Message represents a Telegram message.
type Message struct {
	ID   int64  `json:"message_id"`
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
	Date int64  `json:"date"`
	From *User  `json:"from,omitempty"`
}

// Update represents an incoming update from the Telegram Bot API.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

// APIResponse represents a response from the Telegram Bot API.
// The type parameter T is the type of the result field.
type APIResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description"`
}
