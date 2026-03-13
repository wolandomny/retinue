package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a bare-minimum git repo in a temp directory with one
// commit on main. It returns the repo path and a cleanup function.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	cmds := [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	}
	for _, args := range cmds {
		if _, err := Run(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	// Create an initial commit.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, dir, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestRun_Success(t *testing.T) {
	out, err := Run(context.Background(), ".", "--version")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output from git --version")
	}
}

func TestRunWithEnv_Success(t *testing.T) {
	out, err := RunWithEnv(context.Background(), ".", []string{"GIT_AUTHOR_NAME=TestAuthor"}, "--version")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output from git --version")
	}
}

func TestRun_Failure(t *testing.T) {
	_, err := Run(context.Background(), ".", "nonexistent-subcommand")
	if err == nil {
		t.Fatal("expected error for invalid git subcommand")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestRebaseAndMerge(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	// Create a worktree on a feature branch.
	branch := "feature-branch"
	worktreePath := filepath.Join(t.TempDir(), "wt")

	if _, err := Run(ctx, repoPath, "worktree", "add", "-b", branch, worktreePath); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	// Add a commit on the feature branch inside the worktree.
	if err := os.WriteFile(filepath.Join(worktreePath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// Run RebaseAndMerge.
	if err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", "", ""); err != nil {
		t.Fatalf("RebaseAndMerge failed: %v", err)
	}

	// Verify: the feature commit is now on main.
	log, err := Run(ctx, repoPath, "log", "--oneline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "add feature") {
		t.Errorf("expected 'add feature' commit on main, got log:\n%s", log)
	}

	// Verify: the worktree directory is removed.
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("expected worktree directory %q to be removed", worktreePath)
	}

	// Verify: the branch is deleted.
	branches, err := Run(ctx, repoPath, "branch")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(branches, branch) {
		t.Errorf("expected branch %q to be deleted, got branches:\n%s", branch, branches)
	}
}

func TestRebaseAndMerge_Conflict(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	// Create a worktree on a feature branch.
	branch := "conflict-branch"
	worktreePath := filepath.Join(t.TempDir(), "wt")

	if _, err := Run(ctx, repoPath, "worktree", "add", "-b", branch, worktreePath); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	// Add a conflicting change on main.
	if err := os.WriteFile(filepath.Join(repoPath, "conflict.txt"), []byte("main version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "commit", "-m", "main change"); err != nil {
		t.Fatal(err)
	}

	// Add a conflicting change on the feature branch.
	if err := os.WriteFile(filepath.Join(worktreePath, "conflict.txt"), []byte("branch version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "commit", "-m", "branch change"); err != nil {
		t.Fatal(err)
	}

	// Run RebaseAndMerge — may fail with a conflict error or succeed
	// if a Claude agent is available and resolves the conflict.
	logsDir := t.TempDir()
	err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", logsDir, "")
	if err != nil {
		// When Claude is not available, we expect a rebase conflict error.
		if !strings.Contains(err.Error(), "rebase conflict") && !strings.Contains(err.Error(), "resolution failed") {
			t.Errorf("expected error about rebase conflict or resolution failure, got: %v", err)
		}
	}
	// If err == nil, Claude resolved the conflict — that's also acceptable.
}
