package bus

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// MessageType identifies the kind of bus message.
type MessageType string

const (
	TypeChat   MessageType = "chat"   // conversational message from an agent
	TypeAction MessageType = "action" // agent declaring intent to act
	TypeResult MessageType = "result" // outcome of an action
	TypeUser   MessageType = "user"   // message from the human
	TypeSystem MessageType = "system" // system events (agent joined/left)
)

// Message represents a single entry on the retinue message bus.
type Message struct {
	ID        string            `json:"id"`             // unique hex identifier
	From      string            `json:"from"`           // agent ID, "user", or "system"
	Timestamp time.Time         `json:"ts"`             // when the message was created
	Type      MessageType       `json:"type"`           // message type
	Text      string            `json:"text"`           // message content
	Meta      map[string]string `json:"meta,omitempty"` // optional metadata
}

// NewMessage creates a Message with a generated ID and the current timestamp.
func NewMessage(from string, msgType MessageType, text string) Message {
	return Message{
		ID:        generateID(),
		From:      from,
		Timestamp: time.Now(),
		Type:      msgType,
		Text:      text,
	}
}

// generateID returns a random 32-character hex string (16 random bytes).
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("bus: failed to generate random ID: " + err.Error())
	}
	return hex.EncodeToString(b)
}
