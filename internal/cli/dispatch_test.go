package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
)

func TestResolveWorkDir_NoRepo(t *testing.T) {
	ws := &workspace.Workspace{
		Path: "/tmp/test-apartment",
		Config: workspace.Config{
			Repos: map[string]string{"myrepo": "repos/myrepo"},
		},
	}
	tk := &task.Task{ID: "task-1", Repo: ""}

	dir, err := resolveWorkDir(context.Background(), ws, tk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != ws.Path {
		t.Errorf("expected %q, got %q", ws.Path, dir)
	}
}

func TestResolveWorkDir_UnknownRepo(t *testing.T) {
	ws := &workspace.Workspace{
		Path: "/tmp/test-apartment",
		Config: workspace.Config{
			Repos: map[string]string{"myrepo": "repos/myrepo"},
		},
	}
	tk := &task.Task{ID: "task-1", Repo: "nonexistent"}

	dir, err := resolveWorkDir(context.Background(), ws, tk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != ws.Path {
		t.Errorf("expected %q, got %q", ws.Path, dir)
	}
}

func TestResolveWorkDir_CreatesWorktree(t *testing.T) {
	// Create a temporary "apartment" directory.
	aptDir := t.TempDir()

	// Create a bare-ish git repo to act as the source repo.
	repoDir := filepath.Join(aptDir, "repos", "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Initialize a git repo with an initial commit so worktree add works.
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos: map[string]string{"myrepo": "repos/myrepo"},
		},
	}
	tk := &task.Task{ID: "test-task-1", Repo: "myrepo"}

	dir, err := resolveWorkDir(context.Background(), ws, tk)
	if err != nil {
		t.Fatalf("resolveWorkDir failed: %v", err)
	}

	// Verify the returned path is inside .worktrees.
	expectedDir := filepath.Join(aptDir, ".worktrees", "test-task-1")
	if dir != expectedDir {
		t.Errorf("expected workDir %q, got %q", expectedDir, dir)
	}

	// Verify the .worktrees directory was created.
	if _, err := os.Stat(filepath.Join(aptDir, ".worktrees")); err != nil {
		t.Errorf(".worktrees directory not created: %v", err)
	}

	// Verify the worktree directory exists and is a git checkout.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("worktree directory missing .git: %v", err)
	}

	// Verify the branch name.
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current failed: %v", err)
	}
	branch := string(out)
	// Trim newline.
	branch = branch[:len(branch)-1]
	expected := "retinue/test-task-1"
	if branch != expected {
		t.Errorf("expected branch %q, got %q", expected, branch)
	}
}

func TestResolveWorkDir_WorktreesDirPath(t *testing.T) {
	// Verify that the .worktrees directory path is constructed correctly
	// from the apartment path (without needing a real git repo).
	aptDir := t.TempDir()

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos: map[string]string{"myrepo": "repos/myrepo"},
		},
	}
	tk := &task.Task{ID: "path-check", Repo: "myrepo"}

	// This will fail because there's no git repo, but the .worktrees
	// directory should still be created before the git call.
	_, _ = resolveWorkDir(context.Background(), ws, tk)

	worktreesDir := filepath.Join(aptDir, ".worktrees")
	if _, err := os.Stat(worktreesDir); err != nil {
		t.Errorf(".worktrees directory should be created at %q: %v", worktreesDir, err)
	}
}
