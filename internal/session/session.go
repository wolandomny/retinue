package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ApartmentSession is the tmux session name used within each apartment's
// tmux server. All worker windows live inside this single session.
const ApartmentSession = "retinue"

// Manager manages lifecycle of named terminal sessions.
type Manager interface {
	// Create starts a new detached session running the given command in workDir.
	Create(ctx context.Context, name, workDir, command string) error
	// Kill terminates a session by name. Returns nil if session doesn't exist.
	Kill(ctx context.Context, name string) error
	// Exists reports whether a session with the given name is running.
	Exists(ctx context.Context, name string) (bool, error)
	// Wait blocks until the named session exits.
	//
	// NOTE: This uses `tmux wait-for <name>`, which requires that the command
	// running inside the tmux session signals completion by executing
	// `tmux wait-for -S <name>` before it exits. Without this signal Wait will
	// block indefinitely.
	Wait(ctx context.Context, name string) error

	// CreateWindow creates a named window in the given session. If the session
	// does not exist yet, it creates the session with this window as the first
	// window.
	CreateWindow(ctx context.Context, session, window, workDir, command string) error
	// KillWindow terminates a window by name. Returns nil if the window doesn't exist.
	KillWindow(ctx context.Context, session, window string) error
	// HasWindow reports whether a window with the given name exists in the session.
	HasWindow(ctx context.Context, session, window string) (bool, error)
	// ListWindows returns the names of all windows in the given session.
	// Returns an empty slice if the session does not exist.
	ListWindows(ctx context.Context, session string) ([]string, error)
}

// TmuxManager is a Manager that delegates to the tmux(1) binary.
type TmuxManager struct {
	Socket string // if non-empty, passed as `-L <Socket>` to all tmux commands
}

// NewTmuxManager returns a TmuxManager ready for use.
func NewTmuxManager(socket string) *TmuxManager {
	return &TmuxManager{Socket: socket}
}

// TmuxArgs builds the base tmux argument list, prepending `-L <Socket>`
// when a custom socket name is configured.
func (m *TmuxManager) TmuxArgs(args ...string) []string {
	if m.Socket != "" {
		return append([]string{"-L", m.Socket}, args...)
	}
	return args
}

// Create starts a new detached tmux session named name, with its working
// directory set to workDir, running command.
func (m *TmuxManager) Create(ctx context.Context, name, workDir, command string) error {
	// Write the command to a temp script to avoid tmux's command length limit.
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("retinue-%s.sh", name))
	script := fmt.Sprintf("#!/bin/sh\nrm -f '%s'\n%s\n", scriptPath, command)
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		return fmt.Errorf("writing tmux script: %w", err)
	}

	cmd := exec.CommandContext(ctx, "tmux", m.TmuxArgs("new-session", "-d", "-s", name, "-c", workDir, scriptPath)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(scriptPath)
		return fmt.Errorf("tmux new-session %q: %w: %s", name, err, out)
	}
	return nil
}

// Kill terminates the named tmux session. If the session does not exist (tmux
// exits with code 1) Kill returns nil.
func (m *TmuxManager) Kill(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "tmux", m.TmuxArgs("kill-session", "-t", name)...)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// Session not found — not an error.
			return nil
		}
		return fmt.Errorf("tmux kill-session %q: %w", name, err)
	}
	return nil
}

// Exists reports whether a tmux session with the given name is currently
// running.
func (m *TmuxManager) Exists(ctx context.Context, name string) (bool, error) {
	cmd := exec.CommandContext(ctx, "tmux", m.TmuxArgs("has-session", "-t", name)...)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("tmux has-session %q: %w", name, err)
	}
	return true, nil
}

// Wait blocks until the named tmux session signals completion via
// `tmux wait-for -S <name>`. The command running inside the session must call
// that signal itself; without it Wait will block until ctx is cancelled.
func (m *TmuxManager) Wait(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "tmux", m.TmuxArgs("wait-for", name)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux wait-for %q: %w: %s", name, err, out)
	}
	return nil
}

// CreateWindow creates a named window inside an existing tmux session. If the
// session does not exist, it creates a new session with the window as the
// initial window.
func (m *TmuxManager) CreateWindow(ctx context.Context, session, window, workDir, command string) error {
	// Write the command to a temp script to avoid tmux's command length limit.
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("retinue-%s-%s.sh", session, window))
	script := fmt.Sprintf("#!/bin/sh\nrm -f '%s'\n%s\n", scriptPath, command)
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		return fmt.Errorf("writing tmux script: %w", err)
	}

	exists, err := m.Exists(ctx, session)
	if err != nil {
		os.Remove(scriptPath)
		return err
	}

	if !exists {
		// Session doesn't exist — create it with the window as the first window.
		cmd := exec.CommandContext(ctx, "tmux", m.TmuxArgs(
			"new-session", "-d", "-s", session, "-n", window, "-c", workDir, scriptPath,
		)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			os.Remove(scriptPath)
			return fmt.Errorf("tmux new-session %q (window %q): %w: %s", session, window, err, out)
		}
		return nil
	}

	// Session exists — add a new window.
	target := session + ":"
	cmd := exec.CommandContext(ctx, "tmux", m.TmuxArgs(
		"new-window", "-d", "-t", target, "-n", window, "-c", workDir, scriptPath,
	)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(scriptPath)
		return fmt.Errorf("tmux new-window %q in session %q: %w: %s", window, session, err, out)
	}
	return nil
}

// KillWindow terminates the named window. Returns nil if the window or session
// does not exist.
func (m *TmuxManager) KillWindow(ctx context.Context, session, window string) error {
	target := session + ":" + window
	cmd := exec.CommandContext(ctx, "tmux", m.TmuxArgs("kill-window", "-t", target)...)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil // window/session not found — not an error
		}
		return fmt.Errorf("tmux kill-window %q: %w", target, err)
	}
	return nil
}

// HasWindow reports whether a window with the given name exists in the session.
func (m *TmuxManager) HasWindow(ctx context.Context, session, window string) (bool, error) {
	target := session + ":" + window
	cmd := exec.CommandContext(ctx, "tmux", m.TmuxArgs("list-windows", "-t", session, "-F", "#{window_name}")...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("tmux list-windows for %q: %w", target, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == window {
			return true, nil
		}
	}
	return false, nil
}

// ListWindows returns the names of all windows in the given session.
// Returns an empty slice if the session does not exist.
func (m *TmuxManager) ListWindows(ctx context.Context, session string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "tmux", m.TmuxArgs("list-windows", "-t", session, "-F", "#{window_name}")...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-windows %q: %w", session, err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}
