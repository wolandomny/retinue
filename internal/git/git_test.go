package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wolandomny/retinue/internal/agent"
)

// fakeRunner is a test double for agent.Runner used in git tests.
type fakeRunner struct {
	runFunc func(ctx context.Context, opts agent.RunOpts) (agent.Result, error)
	calls   []agent.RunOpts
}

func (f *fakeRunner) Run(ctx context.Context, opts agent.RunOpts) (agent.Result, error) {
	f.calls = append(f.calls, opts)
	if f.runFunc != nil {
		return f.runFunc(ctx, opts)
	}
	return agent.Result{Output: "fake output", ExitCode: 0}, nil
}

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

	// Run RebaseAndMerge (nil runner — no conflicts expected so runner is unused).
	if err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", "", "", nil); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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
	err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", logsDir, "", nil)
	if err != nil {
		// When Claude is not available, we expect a rebase conflict error.
		if !strings.Contains(err.Error(), "rebase conflict") && !strings.Contains(err.Error(), "resolution failed") {
			t.Errorf("expected error about rebase conflict or resolution failure, got: %v", err)
		}
	}
	// If err == nil, Claude resolved the conflict — that's also acceptable.
}

func TestRebaseAndMerge_ConflictWithMockRunner_Success(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

	// Create a fake runner that resolves conflicts by writing merged content
	// and staging the file, simulating what a real Claude agent would do.
	logsDir := t.TempDir()
	fake := &fakeRunner{
		runFunc: func(ctx context.Context, opts agent.RunOpts) (agent.Result, error) {
			// Simulate Claude resolving the conflict: write merged content
			// and stage the file.
			resolved := "main version\nbranch version\n"
			if err := os.WriteFile(filepath.Join(opts.WorkDir, "conflict.txt"), []byte(resolved), 0o644); err != nil {
				return agent.Result{}, err
			}
			if _, err := Run(ctx, opts.WorkDir, "add", "conflict.txt"); err != nil {
				return agent.Result{}, err
			}
			return agent.Result{Output: "resolved", ExitCode: 0}, nil
		},
	}

	err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", logsDir, "", fake)
	if err != nil {
		t.Fatalf("RebaseAndMerge with mock runner failed: %v", err)
	}

	// Verify the runner was called.
	if len(fake.calls) == 0 {
		t.Fatal("expected fake runner to be called at least once")
	}

	// Verify the merge result: both versions should be present.
	log, err := Run(ctx, repoPath, "log", "--oneline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "branch change") {
		t.Errorf("expected 'branch change' commit on main, got log:\n%s", log)
	}

	// Verify the resolved file content.
	data, err := os.ReadFile(filepath.Join(repoPath, "conflict.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "main version") || !strings.Contains(content, "branch version") {
		t.Errorf("expected merged content with both versions, got: %s", content)
	}
}

func TestRebaseAndMerge_ConflictWithMockRunner_Failure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

	// Create a fake runner that always fails.
	logsDir := t.TempDir()
	fake := &fakeRunner{
		runFunc: func(ctx context.Context, opts agent.RunOpts) (agent.Result, error) {
			return agent.Result{}, fmt.Errorf("mock: agent unavailable")
		},
	}

	err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", logsDir, "", fake)
	if err == nil {
		t.Fatal("expected error when mock runner fails, got nil")
	}
	if !strings.Contains(err.Error(), "rebase conflict") && !strings.Contains(err.Error(), "resolution failed") {
		t.Errorf("expected rebase conflict or resolution failed error, got: %v", err)
	}

	// Verify the runner was called.
	if len(fake.calls) == 0 {
		t.Fatal("expected fake runner to be called at least once")
	}
}
