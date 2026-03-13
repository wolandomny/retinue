package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
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
	if err := rebaseAndMerge(ctx, repoPath, worktreePath, "feature", "main", "", "", nil); err != nil {
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
	if err := rebaseAndMerge(ctx, repoPath, worktreePath, "feature", "develop", "", "", nil); err != nil {
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

func TestMergeOne_NoArchiveKeepsTask(t *testing.T) {
	ctx := context.Background()

	// Create the apartment directory with a repo inside it.
	aptDir := t.TempDir()
	repoRelPath := "repos/myrepo"
	repoPath := filepath.Join(aptDir, repoRelPath)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Initialize the git repo.
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := runGit(ctx, repoPath, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

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
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}

	// Create a worktree for the branch inside the apartment's .worktrees dir.
	worktreeDir := filepath.Join(aptDir, ".worktrees")
	os.MkdirAll(worktreeDir, 0o755)
	worktreePath := filepath.Join(worktreeDir, "t1")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	// Set up workspace and store.
	tasksPath := filepath.Join(aptDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	store.Save([]task.Task{
		{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature"},
	})

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: repoRelPath}},
		},
	}

	result := mergeOne(ctx, mergeOneOpts{
		ws:      ws,
		store:   store,
		t:       task.Task{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature"},
		review:  false,
		archive: false,
		out:     io.Discard,
	})

	if result.Err != nil {
		t.Fatalf("mergeOne failed: %v", result.Err)
	}
	if !result.Merged {
		t.Fatal("expected Merged=true")
	}

	// Task should still be in tasks.yaml (not archived).
	tasks, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task still in file, got %d", len(tasks))
	}
	if tasks[0].Status != task.StatusMerged {
		t.Fatalf("expected merged status, got %s", tasks[0].Status)
	}
}

func TestMarkTaskMergedNoArchive(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "t1", Status: task.StatusDone},
	})
	markTaskMergedNoArchive(store, "t1")

	tasks, _ := store.Load()
	if len(tasks) != 1 {
		t.Fatalf("expected task to remain, got %d tasks", len(tasks))
	}
	if tasks[0].Status != task.StatusMerged {
		t.Fatalf("expected merged, got %s", tasks[0].Status)
	}
	if tasks[0].FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
}

func TestMergeOne_SkipValidation(t *testing.T) {
	ctx := context.Background()

	// Create the apartment directory with a repo inside it.
	aptDir := t.TempDir()
	repoRelPath := "repos/myrepo"
	repoPath := filepath.Join(aptDir, repoRelPath)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Initialize the git repo.
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := runGit(ctx, repoPath, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

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
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}

	// Create a worktree for the branch inside the apartment's .worktrees dir.
	worktreeDir := filepath.Join(aptDir, ".worktrees")
	os.MkdirAll(worktreeDir, 0o755)
	worktreePath := filepath.Join(worktreeDir, "t1")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	// Set up workspace and store.
	tasksPath := filepath.Join(aptDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	store.Save([]task.Task{
		{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature", SkipValidate: true},
	})

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos:    map[string]workspace.RepoConfig{"myrepo": {Path: repoRelPath}},
			Validate: map[string]string{"myrepo": "false"}, // Validation command that would fail
		},
	}

	var output strings.Builder
	result := mergeOne(ctx, mergeOneOpts{
		ws:      ws,
		store:   store,
		t:       task.Task{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature", SkipValidate: true},
		review:  false,
		archive: false,
		out:     &output,
	})

	// Should succeed despite failing validation command because skip_validate=true
	if result.Err != nil {
		t.Fatalf("mergeOne failed: %v", result.Err)
	}
	if !result.Merged {
		t.Fatal("expected Merged=true")
	}

	// Verify the skip message appeared in output
	if !strings.Contains(output.String(), "skipping validation (skip_validate=true)") {
		t.Errorf("expected skip validation message in output, got: %s", output.String())
	}
}

func TestMergeOne_ValidationStillRuns(t *testing.T) {
	ctx := context.Background()

	// Create the apartment directory with a repo inside it.
	aptDir := t.TempDir()
	repoRelPath := "repos/myrepo"
	repoPath := filepath.Join(aptDir, repoRelPath)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Initialize the git repo.
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := runGit(ctx, repoPath, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

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
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}

	// Create a worktree for the branch inside the apartment's .worktrees dir.
	worktreeDir := filepath.Join(aptDir, ".worktrees")
	os.MkdirAll(worktreeDir, 0o755)
	worktreePath := filepath.Join(worktreeDir, "t1")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	// Set up workspace and store.
	tasksPath := filepath.Join(aptDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	store.Save([]task.Task{
		{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature", SkipValidate: false},
	})

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos:    map[string]workspace.RepoConfig{"myrepo": {Path: repoRelPath}},
			Validate: map[string]string{"myrepo": "false"}, // Validation command that will fail
		},
	}

	var output strings.Builder
	result := mergeOne(ctx, mergeOneOpts{
		ws:      ws,
		store:   store,
		t:       task.Task{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature", SkipValidate: false},
		review:  false,
		archive: false,
		out:     &output,
	})

	// Should fail due to validation failure since skip_validate=false
	if result.Err == nil {
		t.Fatal("expected mergeOne to fail due to validation failure")
	}
	if result.Merged {
		t.Fatal("expected Merged=false due to validation failure")
	}

	// Verify the validation failed message appeared in output
	if !strings.Contains(output.String(), "failed validation") {
		t.Errorf("expected validation failure message in output, got: %s", output.String())
	}
}
