package worktree

import (
	"context"
	"fmt"
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
func (m *Manager) Create(ctx context.Context, repoPath, taskID, branch string) (string, error) {
	wtPath := filepath.Join(m.WorktreeDir, taskID)

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
