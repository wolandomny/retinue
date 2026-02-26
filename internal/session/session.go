package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

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
