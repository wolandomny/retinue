package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// pasteSettleDelay is the small pause inserted between paste-buffer
// and send-keys Enter in InjectText, giving the receiving TUI
// (Claude Code) time to finish exiting bracketed-paste mode before
// the Enter keypress arrives. Without this, the Enter is absorbed
// by the paste-mode-exit handler rather than submitting the input.
const pasteSettleDelay = 150 * time.Millisecond

// EscapeTmux escapes a message string for use with tmux send-keys.
// It handles special characters that tmux might interpret.
func EscapeTmux(s string) string {
	// Replace newlines with spaces — send-keys treats Enter as a key literal,
	// so we collapse multi-line input into a single line.
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")

	// Escape backslashes first (before adding more).
	s = strings.ReplaceAll(s, `\`, `\\`)

	// Escape semicolons — tmux uses ; as a command separator.
	s = strings.ReplaceAll(s, ";", `\;`)

	// Escape dollar signs to prevent shell variable expansion.
	s = strings.ReplaceAll(s, "$", `\$`)

	// Escape backticks.
	s = strings.ReplaceAll(s, "`", "\\`")

	return s
}

// injectCounter is a process-unique counter used to generate unique tmux buffer
// names, preventing collisions when multiple goroutines call InjectText concurrently.
var injectCounter atomic.Uint64

// NextBufferName returns a unique tmux buffer name for use by InjectText.
// It incorporates the process PID and an atomically incrementing counter to
// ensure uniqueness across concurrent calls within a process.
func NextBufferName() string {
	n := injectCounter.Add(1)
	return fmt.Sprintf("retinue-inject-%d-%d", os.Getpid(), n)
}

// InjectText delivers text into the given tmux target (e.g. "retinue:woland")
// and submits it with Enter. It uses the load-buffer + paste-buffer + send-keys
// Enter pattern, which is significantly more reliable than "send-keys text Enter"
// for delivering input into a Claude Code TUI session.
//
// baseArgs is the tmux command prefix including any socket flags (e.g.
// ["-L", "retinue-apt"]). It must NOT include "tmux" itself or any subcommand —
// InjectText appends those.
//
// The function performs three sequential tmux invocations:
//  1. tmux <base> load-buffer -b <buf> -         (text via stdin)
//  2. tmux <base> paste-buffer -p -r -d -b <buf> -t <target>
//  3. tmux <base> send-keys -t <target> Enter
//
// Flags explained:
//
//	load-buffer -b <buf> -  : named buffer; read text from stdin (no escaping needed).
//	paste-buffer -p         : wrap in bracketed-paste escape sequences.
//	paste-buffer -r         : preserve LF as LF (default converts to CR).
//	paste-buffer -d         : delete the named buffer after paste.
//	paste-buffer -b <buf>   : use the same named buffer.
//	send-keys Enter         : delivered as a SEPARATE call so it lands outside
//	                          the bracketed-paste sequence and is processed as
//	                          a real submit.
func InjectText(ctx context.Context, baseArgs []string, target, text string) error {
	bufName := NextBufferName()

	// Step 1: Load text into a named tmux buffer via stdin.
	loadArgs := append(append([]string{}, baseArgs...), "load-buffer", "-b", bufName, "-")
	loadCmd := exec.CommandContext(ctx, "tmux", loadArgs...)
	loadCmd.Stdin = strings.NewReader(text)
	if out, err := loadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux load-buffer: %w: %s", err, string(out))
	}

	// Step 2: Paste the buffer into the target pane with bracketed-paste.
	pasteArgs := append(append([]string{}, baseArgs...), "paste-buffer", "-p", "-r", "-d", "-b", bufName, "-t", target)
	pasteCmd := exec.CommandContext(ctx, "tmux", pasteArgs...)
	if out, err := pasteCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux paste-buffer: %w: %s", err, string(out))
	}

	// Brief settle delay: Claude Code's TUI needs a moment to process
	// the bracketed-paste close sequence (\x1b[201~) and exit paste
	// mode before it will accept keystroke input as a real submit.
	// Without this delay, the Enter byte arrives before the
	// paste-mode-exit handler runs and gets absorbed instead of
	// submitting. 150ms is empirically sufficient and imperceptible.
	//
	// Use a context-aware sleep so cancellation is honored.
	select {
	case <-time.After(pasteSettleDelay):
	case <-ctx.Done():
		return ctx.Err()
	}

	// Step 3: Send Enter separately so it is processed as a real submit.
	sendArgs := append(append([]string{}, baseArgs...), "send-keys", "-t", target, "Enter")
	sendCmd := exec.CommandContext(ctx, "tmux", sendArgs...)
	if out, err := sendCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys Enter: %w: %s", err, string(out))
	}

	return nil
}
