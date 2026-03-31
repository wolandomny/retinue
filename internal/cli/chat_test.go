package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

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

	// Verify output format — FormatMessage renders as "[HH:MM:SS] You: text"
	outputStr := output.String()
	if !strings.Contains(outputStr, "You: Hello, world!") {
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
	cmd.SetArgs([]string{"--history=2"})

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

	// Verify the messages are displayed in order — FormatMessage renders
	// "user" as "You" and capitalizes agent names.
	if !strings.Contains(lines[0], "You: Hi there") {
		t.Errorf("First line should contain 'You: Hi there', got: %q", lines[0])
	}
	if !strings.Contains(lines[1], "Agent2: Task completed") {
		t.Errorf("Second line should contain 'Agent2: Task completed', got: %q", lines[1])
	}
}

func TestChatCmd_HistoryDefaultValue(t *testing.T) {
	ws := setupTestWorkspace(t)

	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	// Add more than 50 messages so we can verify the default cap.
	busInstance := bus.New(ws.BusPath())
	for i := 0; i < 60; i++ {
		msg := bus.NewMessage("agent1", bus.TypeChat, fmt.Sprintf("msg-%d", i))
		if err := busInstance.Append(msg); err != nil {
			t.Fatalf("Failed to append message: %v", err)
		}
	}

	cmd := newChatCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	// Pass --history with no number — NoOptDefVal should default to 50.
	cmd.SetArgs([]string{"--history"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Command execution failed: %v", err)
	}

	outputStr := output.String()
	lines := strings.Split(strings.TrimSpace(outputStr), "\n")

	// Default is 50, and we wrote 60 messages — should show exactly 50.
	if len(lines) != 50 {
		t.Errorf("Expected 50 lines of output (default history), got %d", len(lines))
	}

	// The last message should be msg-59 (the most recent).
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "msg-59") {
		t.Errorf("Last line should contain 'msg-59', got: %q", lastLine)
	}

	// The first message should be msg-10 (60 - 50 = 10).
	firstLine := lines[0]
	if !strings.Contains(firstLine, "msg-10") {
		t.Errorf("First line should contain 'msg-10', got: %q", firstLine)
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
	cmd.SetArgs([]string{"--history=10"})

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
	cmd.SetArgs([]string{"--history=10"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Command should handle missing bus file gracefully: %v", err)
	}

	outputStr := output.String()
	if !strings.Contains(outputStr, "No messages yet") {
		t.Errorf("Output should contain helpful message for missing file, got: %q", outputStr)
	}
}

func TestChatCmd_TailShowsRecentMessages(t *testing.T) {
	ws := setupTestWorkspace(t)

	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	// Pre-populate bus with messages so tail's initial output shows them.
	busInstance := bus.New(ws.BusPath())
	msgs := []*bus.Message{
		bus.NewMessage("azazello", bus.TypeChat, "I see a CI failure"),
		bus.NewMessage("user", bus.TypeUser, "Please fix it"),
	}
	for _, msg := range msgs {
		if err := busInstance.Append(msg); err != nil {
			t.Fatalf("Failed to append message: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := newChatCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--tail"})

	// Run the tail command in a goroutine with a cancellable context.
	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.ExecuteContext(ctx)
	}()

	// Give the tailer time to start and print its initial output.
	time.Sleep(800 * time.Millisecond)

	// Write a new message while tailing.
	newMsg := bus.NewMessage("behemoth", bus.TypeChat, "Live message from behemoth")
	if err := busInstance.Append(newMsg); err != nil {
		t.Fatalf("Failed to append live message: %v", err)
	}

	// Wait for the tailer to pick up the new message.
	time.Sleep(1200 * time.Millisecond)

	// Cancel context to stop the tail loop.
	cancel()

	// Wait for the command to finish.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("tail command returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tail command did not exit after cancel")
	}

	// Verify that the initial recent messages are shown.
	outputStr := output.String()
	if !strings.Contains(outputStr, "=== Recent messages ===") {
		t.Errorf("expected '=== Recent messages ===' header, got:\n%s", outputStr)
	}
	if !strings.Contains(outputStr, "Azazello: I see a CI failure") {
		t.Errorf("output should contain Azazello's message, got:\n%s", outputStr)
	}
	if !strings.Contains(outputStr, "You: Please fix it") {
		t.Errorf("output should contain user's message, got:\n%s", outputStr)
	}
	if !strings.Contains(outputStr, "Behemoth: Live message from behemoth") {
		t.Errorf("output should contain live message from Behemoth, got:\n%s", outputStr)
	}
}

func TestChatCmd_TailExitsCleanlyOnCancel(t *testing.T) {
	ws := setupTestWorkspace(t)

	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	ctx, cancel := context.WithCancel(context.Background())

	cmd := newChatCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--tail"})

	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.ExecuteContext(ctx)
	}()

	// Let the tailer start.
	time.Sleep(300 * time.Millisecond)

	// Immediately cancel — should exit without error.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected clean exit, got error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tail command did not exit within timeout")
	}
}

func TestChatCmd_TailEmptyBusShowsWaiting(t *testing.T) {
	ws := setupTestWorkspace(t)

	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	ctx, cancel := context.WithCancel(context.Background())

	cmd := newChatCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--tail"})

	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.ExecuteContext(ctx)
	}()

	// Give time for initial output.
	time.Sleep(800 * time.Millisecond)

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("tail command returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tail command did not exit after cancel")
	}

	outputStr := output.String()
	if !strings.Contains(outputStr, "No messages yet") {
		t.Errorf("expected 'No messages yet' for empty bus, got:\n%s", outputStr)
	}
	if !strings.Contains(outputStr, "Waiting for new messages") {
		t.Errorf("expected 'Waiting for new messages' message, got:\n%s", outputStr)
	}
}

func TestChatCmd_NoArgsShowsHelp(t *testing.T) {
	ws := setupTestWorkspace(t)

	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	cmd := newChatCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	// No args, no flags — should show help.
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected no error when showing help, got: %v", err)
	}

	outputStr := output.String()
	// The help output should contain usage information.
	if !strings.Contains(outputStr, "chat") {
		t.Errorf("expected help output to contain 'chat', got:\n%s", outputStr)
	}
	if !strings.Contains(outputStr, "--tail") {
		t.Errorf("expected help output to mention --tail flag, got:\n%s", outputStr)
	}
	if !strings.Contains(outputStr, "--history") {
		t.Errorf("expected help output to mention --history flag, got:\n%s", outputStr)
	}
}

func TestChatCmd_SendAndFlagsConflict(t *testing.T) {
	ws := setupTestWorkspace(t)

	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	// Sending a message with --tail should fail.
	cmd := newChatCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--tail", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when combining message with --tail")
	}
	if !strings.Contains(err.Error(), "cannot combine") {
		t.Errorf("expected 'cannot combine' error, got: %v", err)
	}
}