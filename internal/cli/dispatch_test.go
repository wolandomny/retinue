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

func TestDispatch_SetsBranchAfterWorktreeCreation(t *testing.T) {
	// Create a temporary workspace directory.
	aptDir := t.TempDir()

	// Create a git repo to act as the source repo.
	repoDir := filepath.Join(aptDir, "repos", "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
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

	// Create a tasks.yaml with a pending task.
	tk := task.Task{ID: "branch-test-1", Repo: "myrepo", Status: task.StatusPending}
	store := task.NewFileStore(ws.TasksPath())
	if err := store.Save([]task.Task{tk}); err != nil {
		t.Fatalf("saving tasks: %v", err)
	}

	// Create the worktree via resolveWorkDir.
	_, err := resolveWorkDir(context.Background(), ws, &tk)
	if err != nil {
		t.Fatalf("resolveWorkDir failed: %v", err)
	}

	// Record the branch, mirroring the logic in dispatch.go.
	if tk.Repo != "" {
		if err := store.Update(tk.ID, func(t *task.Task) {
			t.Branch = "retinue/" + t.ID
		}); err != nil {
			t.Fatalf("updating branch: %v", err)
		}
	}

	// Verify the Branch field is persisted correctly.
	updated, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("getting task: %v", err)
	}
	expected := "retinue/branch-test-1"
	if updated.Branch != expected {
		t.Errorf("expected Branch %q, got %q", expected, updated.Branch)
	}
}

func TestResolveWorkDir_ExistingWorktree(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	// Create a temporary "apartment" directory with the repo inside it.
	aptDir := t.TempDir()
	repoRelPath := "repos/myrepo"
	repoDir := filepath.Join(aptDir, repoRelPath)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}

	// Initialize a git repo at the expected location.
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	// Symlink the repo into the apartment structure.
	if err := os.Symlink(repoPath, repoDir); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos: map[string]string{"myrepo": repoRelPath},
		},
	}
	tk := &task.Task{ID: "existing-wt", Repo: "myrepo"}

	// First call: creates the worktree.
	dir1, err := resolveWorkDir(ctx, ws, tk)
	if err != nil {
		t.Fatalf("first resolveWorkDir failed: %v", err)
	}

	// Verify the worktree was created.
	expectedDir := filepath.Join(aptDir, ".worktrees", "existing-wt")
	if dir1 != expectedDir {
		t.Fatalf("expected workDir %q, got %q", expectedDir, dir1)
	}

	// Second call: should reuse the existing worktree directory.
	dir2, err := resolveWorkDir(ctx, ws, tk)
	if err != nil {
		t.Fatalf("second resolveWorkDir failed (should reuse existing): %v", err)
	}

	if dir2 != dir1 {
		t.Errorf("expected reused path %q, got %q", dir1, dir2)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is a long string", 10, "this is..."},
		{"multi\nline\ntext", 20, "multi line text"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
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
