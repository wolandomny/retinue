package shell

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

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
			name:  "backslashes escaped first",
			input: `path\to\file`,
			want:  `path\\to\\file`,
		},
		{
			name:  "semicolons escaped",
			input: "foo; bar; baz",
			want:  `foo\; bar\; baz`,
		},
		{
			name:  "dollar signs escaped",
			input: "echo $HOME $USER",
			want:  `echo \$HOME \$USER`,
		},
		{
			name:  "backticks escaped",
			input: "run `command`",
			want:  "run \\`command\\`",
		},
		{
			name:  "newlines collapsed to spaces",
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
		{
			name:  "backslash before semicolon",
			input: `\;`,
			want:  `\\\;`,
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

func TestNextBufferName_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		name := NextBufferName()
		if seen[name] {
			t.Fatalf("NextBufferName() returned duplicate name %q on iteration %d", name, i)
		}
		seen[name] = true
	}
}

func TestNextBufferName_Format(t *testing.T) {
	name := NextBufferName()
	if !strings.HasPrefix(name, "retinue-inject-") {
		t.Errorf("NextBufferName() = %q, want prefix %q", name, "retinue-inject-")
	}
	// Should contain at least two dashes after the prefix (pid-counter).
	parts := strings.Split(name, "-")
	// "retinue" - "inject" - "<pid>" - "<counter>" = at least 4 parts
	if len(parts) < 4 {
		t.Errorf("NextBufferName() = %q, expected at least 4 dash-separated parts", name)
	}
}

func TestInjectText_Integration(t *testing.T) {
	// Skip if tmux is not available.
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH, skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	socketName := "retinue-test-inject"
	sessionName := "inject-test"

	// Create a temporary tmux session with a long-lived shell.
	createCmd := exec.CommandContext(ctx, "tmux", "-L", socketName, "new-session", "-d", "-s", sessionName, "-x", "200", "-y", "50")
	if out, err := createCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create tmux session: %v: %s", err, string(out))
	}

	// Ensure cleanup of the tmux server.
	defer func() {
		killCmd := exec.Command("tmux", "-L", socketName, "kill-server")
		_ = killCmd.Run()
	}()

	// Give the session a moment to initialize.
	time.Sleep(200 * time.Millisecond)

	testText := "hello from InjectText test"
	baseArgs := []string{"-L", socketName}
	target := sessionName + ":"

	err := InjectText(ctx, baseArgs, target, testText)
	if err != nil {
		t.Fatalf("InjectText() returned error: %v", err)
	}

	// Give tmux a moment to process the paste and Enter.
	time.Sleep(300 * time.Millisecond)

	// Capture the pane content and verify our text appeared.
	captureCmd := exec.CommandContext(ctx, "tmux", "-L", socketName, "capture-pane", "-p", "-t", target)
	out, err := captureCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("capture-pane failed: %v: %s", err, string(out))
	}

	if !strings.Contains(string(out), testText) {
		t.Errorf("expected pane to contain %q, got:\n%s", testText, string(out))
	}
}

func TestInjectText_SpecialChars_Integration(t *testing.T) {
	// Skip if tmux is not available.
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH, skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	socketName := "retinue-test-inject-special"
	sessionName := "inject-test-special"

	createCmd := exec.CommandContext(ctx, "tmux", "-L", socketName, "new-session", "-d", "-s", sessionName, "-x", "200", "-y", "50")
	if out, err := createCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create tmux session: %v: %s", err, string(out))
	}

	defer func() {
		killCmd := exec.Command("tmux", "-L", socketName, "kill-server")
		_ = killCmd.Run()
	}()

	time.Sleep(200 * time.Millisecond)

	// Text with characters that would need escaping under send-keys but NOT
	// under load-buffer (semicolons, dollar signs, backticks, backslashes).
	testText := `echo $HOME; ls \path; run` + "`cmd`"
	baseArgs := []string{"-L", socketName}
	target := sessionName + ":"

	err := InjectText(ctx, baseArgs, target, testText)
	if err != nil {
		t.Fatalf("InjectText() returned error: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	captureCmd := exec.CommandContext(ctx, "tmux", "-L", socketName, "capture-pane", "-p", "-t", target)
	out, err := captureCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("capture-pane failed: %v: %s", err, string(out))
	}

	if !strings.Contains(string(out), testText) {
		t.Errorf("expected pane to contain %q, got:\n%s", testText, string(out))
	}
}
