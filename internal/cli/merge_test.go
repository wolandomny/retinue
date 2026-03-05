package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/task"
)

func TestRunValidation_NoConfig(t *testing.T) {
	// nil validate map → should return nil (no validation).
	err := runValidation(context.Background(), t.TempDir(), "myrepo", nil)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunValidation_EmptyCommand(t *testing.T) {
	// Empty string command → should return nil.
	err := runValidation(context.Background(), t.TempDir(), "myrepo",
		map[string]string{"myrepo": ""})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunValidation_RepoNotInMap(t *testing.T) {
	// Repo not in validate map → should return nil.
	err := runValidation(context.Background(), t.TempDir(), "other",
		map[string]string{"myrepo": "true"})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunValidation_Success(t *testing.T) {
	// Command succeeds → should return nil.
	err := runValidation(context.Background(), t.TempDir(), "myrepo",
		map[string]string{"myrepo": "true"})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunValidation_Failure(t *testing.T) {
	// Command fails → should return error containing "validation failed".
	err := runValidation(context.Background(), t.TempDir(), "myrepo",
		map[string]string{"myrepo": "false"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestRunValidation_OutputIncluded(t *testing.T) {
	// Verify that command output is included in the error message.
	err := runValidation(context.Background(), t.TempDir(), "r",
		map[string]string{"r": "echo 'build broke' && exit 1"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "build broke") {
		t.Fatalf("expected output in error, got: %s", err)
	}
}

func TestMarkTaskFailed_SetsStatusAndError(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "t1", Status: task.StatusDone},
	})
	markTaskFailed(store, "t1", "something broke")

	tasks, _ := store.Load()
	if tasks[0].Status != task.StatusFailed {
		t.Fatalf("expected failed, got %s", tasks[0].Status)
	}
	if tasks[0].Error != "something broke" {
		t.Fatalf("expected error message, got %q", tasks[0].Error)
	}
	if tasks[0].FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
}

func TestRebaseAndMerge_FastForwardOnly(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	// Create a branch with a commit.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// Go back to main in the repo before creating worktree (branch is
	// already checked out in the main repo, so we must detach first).
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}

	// Create a worktree for the branch.
	worktreePath := filepath.Join(t.TempDir(), "wt")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	// Merge via rebaseAndMerge.
	if err := rebaseAndMerge(ctx, repoPath, worktreePath, "feature", "main", "", ""); err != nil {
		t.Fatalf("rebaseAndMerge failed: %v", err)
	}

	// Verify HEAD has exactly one parent (fast-forward, not merge commit).
	parents, err := runGit(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(parents)
	if len(parts) > 2 {
		t.Fatalf("expected fast-forward (1 parent), got %d parents", len(parts)-1)
	}
}

func TestRebaseAndMerge_CustomBaseBranch(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	// Create a "develop" branch from main.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "develop"); err != nil {
		t.Fatal(err)
	}
	// Add a commit to develop.
	if err := os.WriteFile(filepath.Join(repoPath, "develop.txt"), []byte("develop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "develop base"); err != nil {
		t.Fatal(err)
	}

	// Create a feature branch off develop.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// Go back to develop.
	if _, err := runGit(ctx, repoPath, "checkout", "develop"); err != nil {
		t.Fatal(err)
	}

	// Create worktree for feature.
	worktreePath := filepath.Join(t.TempDir(), "wt")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	// Merge feature into develop (not main).
	if err := rebaseAndMerge(ctx, repoPath, worktreePath, "feature", "develop", "", ""); err != nil {
		t.Fatalf("rebaseAndMerge to develop failed: %v", err)
	}

	// Verify we're on develop and it has the feature commit.
	branch, err := runGit(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if branch != "develop" {
		t.Errorf("expected to be on 'develop', got %q", branch)
	}

	// Verify feature.txt exists on develop.
	if _, err := os.Stat(filepath.Join(repoPath, "feature.txt")); os.IsNotExist(err) {
		t.Error("feature.txt should exist on develop after merge")
	}

	// Verify main is unchanged (doesn't have feature.txt).
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoPath, "feature.txt")); !os.IsNotExist(err) {
		t.Error("feature.txt should NOT exist on main")
	}
}

func TestMarkTaskMerged_SetsStatus(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")
	archivePath := filepath.Join(dir, "tasks-archive.yaml")

	store := task.NewFileStore(tasksPath)
	store.Save([]task.Task{
		{ID: "t1", Status: task.StatusDone},
	})
	markTaskMerged(store, "t1", archivePath)

	// Task should be removed from the main file (archived).
	tasks, _ := store.Load()
	if len(tasks) != 0 {
		t.Fatalf("expected 0 remaining tasks, got %d", len(tasks))
	}

	// Task should be in the archive with merged status.
	archiveStore := task.NewFileStore(archivePath)
	archived, _ := archiveStore.Load()
	if len(archived) != 1 {
		t.Fatalf("expected 1 archived task, got %d", len(archived))
	}
	if archived[0].Status != task.StatusMerged {
		t.Fatalf("expected merged, got %s", archived[0].Status)
	}
	if archived[0].FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
}
