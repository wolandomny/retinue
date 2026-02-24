package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GitExecutor abstracts git command execution for testability.
type GitExecutor interface {
	Exec(ctx context.Context, dir string, args ...string) (string, error)
}

// RealGit executes actual git commands.
type RealGit struct{}

func (g *RealGit) Exec(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), stderr.String(), err)
	}

	return strings.TrimSpace(stdout.String()), nil
}
