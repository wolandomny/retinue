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
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Type      MessageType            `json:"type"`
	Text      string                 `json:"text"`
	To        []string               `json:"to,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Meta      map[string]interface{} `json:"meta,omitempty"`
}

// NewMessage creates a Message with a generated ID and the current timestamp.
func NewMessage(name string, msgType MessageType, text string) *Message {
	return &Message{
		ID:        generateID(),
		Name:      name,
		Type:      msgType,
		Text:      text,
		Timestamp: time.Now(),
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
