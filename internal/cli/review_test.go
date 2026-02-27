package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/task"
)

func TestRunGit_Success(t *testing.T) {
	out, err := runGit(context.Background(), ".", "--version")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output from git --version")
	}
}

func TestRunGitWithEnv_Success(t *testing.T) {
	out, err := runGitWithEnv(context.Background(), ".", []string{"GIT_AUTHOR_NAME=TestAuthor"}, "--version")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output from git --version")
	}
}

func TestRunGit_Failure(t *testing.T) {
	_, err := runGit(context.Background(), ".", "nonexistent-subcommand")
	if err == nil {
		t.Fatal("expected error for invalid git subcommand")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestIsApproved(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"exact", "APPROVED", true},
		{"with message", "APPROVED\nLooks good", true},
		{"leading whitespace", "  APPROVED  ", true},
		{"rejected", "REJECTED\nbad code", false},
		{"empty", "", false},
		{"lowercase", "approved", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isApproved(tt.input)
			if got != tt.expect {
				t.Errorf("isApproved(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestFindReviewable(t *testing.T) {
	tests := []struct {
		name   string
		tasks  []task.Task
		expect *string // nil means no task found, otherwise the expected task ID
	}{
		{
			name:   "no tasks",
			tasks:  nil,
			expect: nil,
		},
		{
			name: "wrong status",
			tasks: []task.Task{
				{ID: "a", Status: task.StatusPending, Branch: "retinue/a", Repo: "myrepo"},
				{ID: "b", Status: task.StatusInProgress, Branch: "retinue/b", Repo: "myrepo"},
			},
			expect: nil,
		},
		{
			name: "done but no branch",
			tasks: []task.Task{
				{ID: "a", Status: task.StatusDone, Branch: "", Repo: "myrepo"},
			},
			expect: nil,
		},
		{
			name: "done but no repo",
			tasks: []task.Task{
				{ID: "a", Status: task.StatusDone, Branch: "retinue/a", Repo: ""},
			},
			expect: nil,
		},
		{
			name: "done with branch and repo",
			tasks: []task.Task{
				{ID: "skip", Status: task.StatusPending, Branch: "retinue/skip", Repo: "myrepo"},
				{ID: "target", Status: task.StatusDone, Branch: "retinue/target", Repo: "myrepo"},
				{ID: "other", Status: task.StatusDone, Branch: "retinue/other", Repo: "myrepo"},
			},
			expect: strPtr("target"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findReviewable(tt.tasks)
			if tt.expect == nil {
				if got != nil {
					t.Errorf("expected nil, got task %q", got.ID)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected task %q, got nil", *tt.expect)
			}
			if got.ID != *tt.expect {
				t.Errorf("expected task %q, got %q", *tt.expect, got.ID)
			}
		})
	}
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
		if _, err := runGit(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	// Create an initial commit.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestRebaseAndMerge(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	// Create a worktree on a feature branch.
	branch := "feature-branch"
	worktreePath := filepath.Join(t.TempDir(), "wt")

	if _, err := runGit(ctx, repoPath, "worktree", "add", "-b", branch, worktreePath); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	// Add a commit on the feature branch inside the worktree.
	if err := os.WriteFile(filepath.Join(worktreePath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, worktreePath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, worktreePath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// Run rebaseAndMerge.
	if err := rebaseAndMerge(ctx, repoPath, worktreePath, branch, "", ""); err != nil {
		t.Fatalf("rebaseAndMerge failed: %v", err)
	}

	// Verify: the feature commit is now on main.
	log, err := runGit(ctx, repoPath, "log", "--oneline")
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
	branches, err := runGit(ctx, repoPath, "branch")
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

	if _, err := runGit(ctx, repoPath, "worktree", "add", "-b", branch, worktreePath); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	// Add a conflicting change on main.
	if err := os.WriteFile(filepath.Join(repoPath, "conflict.txt"), []byte("main version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "main change"); err != nil {
		t.Fatal(err)
	}

	// Add a conflicting change on the feature branch.
	if err := os.WriteFile(filepath.Join(worktreePath, "conflict.txt"), []byte("branch version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, worktreePath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, worktreePath, "commit", "-m", "branch change"); err != nil {
		t.Fatal(err)
	}

	// Run rebaseAndMerge — may fail with a conflict error or succeed
	// if a Claude agent is available and resolves the conflict.
	logsDir := t.TempDir()
	err := rebaseAndMerge(ctx, repoPath, worktreePath, branch, "", logsDir)
	if err != nil {
		// When Claude is not available, we expect a rebase conflict error.
		if !strings.Contains(err.Error(), "rebase conflict") && !strings.Contains(err.Error(), "resolution failed") {
			t.Errorf("expected error about rebase conflict or resolution failure, got: %v", err)
		}
	}
	// If err == nil, Claude resolved the conflict — that's also acceptable.
}

// writeTasks creates a tasks.yaml with the given tasks and returns a FileStore.
func writeTasks(t *testing.T, tasks []task.Task) *task.FileStore {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")
	store := task.NewFileStore(path)
	if err := store.Save(tasks); err != nil {
		t.Fatalf("saving tasks: %v", err)
	}
	return store
}

func TestMarkTaskFailed(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "task-1", Status: task.StatusReview},
	})

	markTaskFailed(store, "task-1", "something went wrong")

	got, err := store.Get("task-1")
	if err != nil {
		t.Fatalf("loading task: %v", err)
	}
	if got.Status != task.StatusFailed {
		t.Errorf("expected status %q, got %q", task.StatusFailed, got.Status)
	}
	if got.Error != "something went wrong" {
		t.Errorf("expected error %q, got %q", "something went wrong", got.Error)
	}
	if got.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}
}

func TestMarkTaskMerged(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "task-1", Status: task.StatusReview},
	})

	markTaskMerged(store, "task-1")

	got, err := store.Get("task-1")
	if err != nil {
		t.Fatalf("loading task: %v", err)
	}
	if got.Status != task.StatusMerged {
		t.Errorf("expected status %q, got %q", task.StatusMerged, got.Status)
	}
	if got.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}
}

func strPtr(s string) *string {
	return &s
}
