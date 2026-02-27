package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Manager handles git worktree lifecycle.
type Manager struct {
	Git         GitExecutor
	WorktreeDir string // base directory for worktrees
}

func NewManager(git GitExecutor, worktreeDir string) *Manager {
	return &Manager{Git: git, WorktreeDir: worktreeDir}
}

// Create creates a new worktree for the given task in the given repo.
// If the worktree directory already exists, it is reused without
// trying to create a new one (which would fail).
func (m *Manager) Create(ctx context.Context, repoPath, taskID, branch string) (string, error) {
	wtPath := filepath.Join(m.WorktreeDir, taskID)

	// If the worktree directory already exists, reuse it.
	if info, err := os.Stat(wtPath); err == nil && info.IsDir() {
		return wtPath, nil
	}

	if branch == "" {
		branch = "retinue/" + taskID
	}

	// Create a new branch and worktree.
	_, err := m.Git.Exec(ctx, repoPath, "worktree", "add", "-b", branch, wtPath)
	if err != nil {
		return "", fmt.Errorf("creating worktree for %s: %w", taskID, err)
	}

	return wtPath, nil
}

// Remove removes a worktree for the given task.
func (m *Manager) Remove(ctx context.Context, repoPath, taskID string) error {
	wtPath := filepath.Join(m.WorktreeDir, taskID)

	_, err := m.Git.Exec(ctx, repoPath, "worktree", "remove", wtPath)
	if err != nil {
		return fmt.Errorf("removing worktree for %s: %w", taskID, err)
	}

	return nil
}
