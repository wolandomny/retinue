package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/bus"
	"github.com/wolandomny/retinue/internal/workspace"
)

// setupTestWorkspace creates a temporary workspace for testing.
func setupTestWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()

	tmpDir := t.TempDir()

	cfg := workspace.Config{
		Model:      "claude-opus-4-6",
		MaxWorkers: 1,
	}

	ws, err := workspace.Create(tmpDir, cfg)
	if err != nil {
		t.Fatalf("Failed to create test workspace: %v", err)
	}

	return ws
}

func TestChatCmd_Send(t *testing.T) {
	ws := setupTestWorkspace(t)

	// Override workspace detection to use our test workspace
	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	cmd := newChatCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"Hello, world!"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Command execution failed: %v", err)
	}

	// Verify the message was written to the bus
	busInstance := bus.New(ws.BusPath())
	messages, err := busInstance.ReadRecent(1)
	if err != nil {
		t.Fatalf("Failed to read messages: %v", err)
	}

	if len(messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(messages))
	}

	msg := messages[0]
	if msg.Name != "user" {
		t.Errorf("Expected message name 'user', got %q", msg.Name)
	}
	if msg.Type != bus.TypeUser {
		t.Errorf("Expected message type %q, got %q", bus.TypeUser, msg.Type)
	}
	if msg.Text != "Hello, world!" {
		t.Errorf("Expected message text 'Hello, world!', got %q", msg.Text)
	}

	// Verify output format
	outputStr := output.String()
	if !strings.Contains(outputStr, "user: Hello, world!") {
		t.Errorf("Output should contain formatted message, got: %q", outputStr)
	}
}

func TestChatCmd_History(t *testing.T) {
	ws := setupTestWorkspace(t)

	// Override workspace detection to use our test workspace
	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	// Add some test messages to the bus
	busInstance := bus.New(ws.BusPath())

	messages := []*bus.Message{
		bus.NewMessage("agent1", bus.TypeChat, "Hello"),
		bus.NewMessage("user", bus.TypeUser, "Hi there"),
		bus.NewMessage("agent2", bus.TypeResult, "Task completed"),
	}

	for _, msg := range messages {
		if err := busInstance.Append(msg); err != nil {
			t.Fatalf("Failed to append message: %v", err)
		}
	}

	cmd := newChatCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--history", "2"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Command execution failed: %v", err)
	}

	outputStr := output.String()
	lines := strings.Split(strings.TrimSpace(outputStr), "\n")

	// Should show the last 2 messages
	if len(lines) != 2 {
		t.Errorf("Expected 2 lines of output, got %d: %v", len(lines), lines)
	}

	// Verify the messages are displayed in order
	if !strings.Contains(lines[0], "user: Hi there") {
		t.Errorf("First line should contain 'user: Hi there', got: %q", lines[0])
	}
	if !strings.Contains(lines[1], "agent2: Task completed") {
		t.Errorf("Second line should contain 'agent2: Task completed', got: %q", lines[1])
	}
}

func TestChatCmd_HistoryEmpty(t *testing.T) {
	ws := setupTestWorkspace(t)

	// Override workspace detection to use our test workspace
	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	cmd := newChatCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--history", "10"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Command execution failed: %v", err)
	}

	outputStr := output.String()
	if !strings.Contains(outputStr, "No messages yet") {
		t.Errorf("Output should contain 'No messages yet' for empty bus, got: %q", outputStr)
	}
}

func TestChatCmd_BusFileNotExists(t *testing.T) {
	ws := setupTestWorkspace(t)

	// Override workspace detection to use our test workspace
	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	// Remove the bus file to test graceful handling
	busPath := ws.BusPath()
	if err := os.Remove(busPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Failed to remove bus file: %v", err)
	}

	cmd := newChatCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--history", "10"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Command should handle missing bus file gracefully: %v", err)
	}

	outputStr := output.String()
	if !strings.Contains(outputStr, "No messages yet") {
		t.Errorf("Output should contain helpful message for missing file, got: %q", outputStr)
	}
}