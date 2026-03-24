package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
)

func TestSyncWriter_ConcurrentWrites(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	sw := &syncWriter{mu: &mu, w: &buf}

	const goroutines = 10
	const writes = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < writes; j++ {
				fmt.Fprintf(sw, "goroutine %d write %d\n", id, j)
			}
		}(i)
	}

	wg.Wait()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != goroutines*writes {
		t.Errorf("expected %d lines, got %d", goroutines*writes, len(lines))
	}

	// Verify no interleaved output — each line should match the pattern.
	for i, line := range lines {
		if !strings.HasPrefix(line, "goroutine ") || !strings.Contains(line, " write ") {
			t.Errorf("line %d looks corrupted: %q", i, line)
		}
	}
}

func TestSyncWriter_ImplementsIOWriter(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	sw := &syncWriter{mu: &mu, w: &buf}

	// Verify it satisfies io.Writer interface.
	var w io.Writer = sw
	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes written, got %d", n)
	}
	if buf.String() != "hello" {
		t.Fatalf("expected 'hello', got %q", buf.String())
	}
}

func TestPrintRunSummary(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")
	archivePath := filepath.Join(dir, "tasks-archive.yaml")

	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "t1", Status: task.StatusFailed, Meta: map[string]string{"cost_usd": "1.5000"}},
		{ID: "t2", Status: task.StatusPending, Meta: map[string]string{"cost_usd": "0.0000"}},
		{ID: "t3", Status: task.StatusMerged, Meta: map[string]string{"cost_usd": "2.2500"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Write an archived task too.
	archiveStore := task.NewFileStore(archivePath)
	if err := archiveStore.Save([]task.Task{
		{ID: "t0", Status: task.StatusMerged, Meta: map[string]string{"cost_usd": "0.7500"}},
	}); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{Path: dir, Config: workspace.Config{TrackCosts: true}}

	var buf bytes.Buffer
	printRunSummary(ws, store, &buf)

	output := buf.String()

	// Should have 2 merged (t3 active + t0 archived), 1 failed, 1 pending.
	if !strings.Contains(output, "2 merged") {
		t.Errorf("expected '2 merged' in output, got: %s", output)
	}
	if !strings.Contains(output, "1 failed") {
		t.Errorf("expected '1 failed' in output, got: %s", output)
	}
	if !strings.Contains(output, "1 pending") {
		t.Errorf("expected '1 pending' in output, got: %s", output)
	}
	// Total cost: 1.5 + 0.0 + 2.25 + 0.75 = 4.50
	if !strings.Contains(output, "$4.50") {
		t.Errorf("expected '$4.50' in output, got: %s", output)
	}
}

func TestPrintRunSummary_TrackCostsDisabled(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")

	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "t1", Status: task.StatusFailed, Meta: map[string]string{"cost_usd": "1.5000"}},
		{ID: "t2", Status: task.StatusMerged},
	}); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{Path: dir} // TrackCosts defaults to false

	var buf bytes.Buffer
	printRunSummary(ws, store, &buf)

	output := buf.String()
	if !strings.Contains(output, "1 merged") {
		t.Errorf("expected '1 merged' in output, got: %s", output)
	}
	if !strings.Contains(output, "1 failed") {
		t.Errorf("expected '1 failed' in output, got: %s", output)
	}
	if strings.Contains(output, "Total cost") {
		t.Errorf("expected no 'Total cost' in output when track_costs is off, got: %s", output)
	}
	if strings.Contains(output, "$") {
		t.Errorf("expected no dollar sign in output when track_costs is off, got: %s", output)
	}
}

func TestPrintRunSummary_NoArchive(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")

	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "t1", Status: task.StatusMerged},
	}); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{Path: dir, Config: workspace.Config{TrackCosts: true}}

	var buf bytes.Buffer
	printRunSummary(ws, store, &buf)

	output := buf.String()
	if !strings.Contains(output, "1 merged") {
		t.Errorf("expected '1 merged' in output, got: %s", output)
	}
	if !strings.Contains(output, "$0.00") {
		t.Errorf("expected '$0.00' in output, got: %s", output)
	}
}

func TestArchiveCleanup(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")
	archivePath := filepath.Join(dir, "tasks-archive.yaml")

	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "t1", Status: task.StatusMerged},
		{ID: "t2", Status: task.StatusFailed},
		{ID: "t3", Status: task.StatusMerged},
	}); err != nil {
		t.Fatal(err)
	}

	// Archive all merged tasks (same logic as post-loop cleanup).
	tasks, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var toArchive []string
	for _, tk := range tasks {
		if tk.Status == task.StatusMerged {
			toArchive = append(toArchive, tk.ID)
		}
	}
	for _, id := range toArchive {
		if err := store.Archive(id, archivePath); err != nil {
			t.Fatalf("archive %q: %v", id, err)
		}
	}

	// Verify: only the failed task should remain in the active store.
	remaining, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining task, got %d", len(remaining))
	}
	if remaining[0].ID != "t2" {
		t.Fatalf("expected t2 to remain, got %s", remaining[0].ID)
	}

	// Verify: 2 tasks should be in the archive.
	archiveStoreCheck := task.NewFileStore(archivePath)
	archived, err := archiveStoreCheck.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 2 {
		t.Fatalf("expected 2 archived tasks, got %d", len(archived))
	}
}

func TestBuildDependencyContext_WithDeps(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "dep1", Status: task.StatusMerged, Description: "Setup DB", Result: "Created schema"},
		{ID: "dep2", Status: task.StatusMerged, Description: "Add auth", Result: "Added JWT auth"},
		{ID: "main-task", Status: task.StatusPending, DependsOn: []string{"dep1", "dep2"}},
	})

	ctx := buildDependencyContext(store, []string{"dep1", "dep2"})

	if !strings.Contains(ctx, "dep1") {
		t.Errorf("expected dep1 in context, got: %s", ctx)
	}
	if !strings.Contains(ctx, "Setup DB") {
		t.Errorf("expected description in context, got: %s", ctx)
	}
	if !strings.Contains(ctx, "Created schema") {
		t.Errorf("expected result in context, got: %s", ctx)
	}
	if !strings.Contains(ctx, "dep2") {
		t.Errorf("expected dep2 in context, got: %s", ctx)
	}
}

func TestNewRunCmd_Flags(t *testing.T) {
	cmd := newRunCmd()

	if cmd.Use != "run" {
		t.Errorf("expected Use 'run', got %q", cmd.Use)
	}

	retry := cmd.Flags().Lookup("retry")
	if retry == nil {
		t.Fatal("expected --retry flag")
	}

	maxRetries := cmd.Flags().Lookup("max-retries")
	if maxRetries == nil {
		t.Fatal("expected --max-retries flag")
	}
	if maxRetries.DefValue != "2" {
		t.Errorf("expected default 2 for --max-retries, got %s", maxRetries.DefValue)
	}

	reviewFlag := cmd.Flags().Lookup("review")
	if reviewFlag == nil {
		t.Fatal("expected --review flag")
	}
}

func TestParseBisectOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "standard output",
			output: "abc123def456 is the first bad commit\ncommit abc123def456\nAuthor: Test",
			want:   "abc123def456",
		},
		{
			name:   "full hash",
			output: "running sh -c test -f valid.txt\n1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b is the first bad commit\n",
			want:   "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
		},
		{
			name:   "no bad commit",
			output: "Bisecting: 3 revisions left to test\n",
			want:   "",
		},
		{
			name:   "empty output",
			output: "",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBisectOutput(tt.output)
			if got != tt.want {
				t.Errorf("parseBisectOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

// initTestRepoAt creates a git repo at the specified directory with
// one commit on main containing README.md.
func initTestRepoAt(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := runGit(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
}

func TestBisectAndRevert(t *testing.T) {
	ctx := context.Background()

	// Set up workspace directory structure.
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repos", "myrepo")
	initTestRepoAt(t, repoDir)

	// Add valid.txt so initial state passes validation.
	if err := os.WriteFile(filepath.Join(repoDir, "valid.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "commit", "-m", "add valid.txt"); err != nil {
		t.Fatal(err)
	}
	initialHead, err := runGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Task1 commit: add file1.txt (validation still passes).
	if err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("from task1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "commit", "-m", "task1: add file1"); err != nil {
		t.Fatal(err)
	}
	commitA, err := runGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Task2 commit: remove valid.txt (validation fails!).
	if err := os.Remove(filepath.Join(repoDir, "valid.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "commit", "-m", "task2: remove valid.txt"); err != nil {
		t.Fatal(err)
	}
	commitB, err := runGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Task3 commit: add file2.txt (validation still fails).
	if err := os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("from task3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "commit", "-m", "task3: add file2"); err != nil {
		t.Fatal(err)
	}
	commitC, err := runGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Create merge records mapping commits to tasks.
	records := []mergeRecord{
		{taskID: "task1", repo: "myrepo", commit: commitA},
		{taskID: "task2", repo: "myrepo", commit: commitB},
		{taskID: "task3", repo: "myrepo", commit: commitC},
	}

	// Create task store with all tasks as merged.
	tasksPath := filepath.Join(wsDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "task1", Status: task.StatusMerged, Repo: "myrepo"},
		{ID: "task2", Status: task.StatusMerged, Repo: "myrepo"},
		{ID: "task3", Status: task.StatusMerged, Repo: "myrepo"},
	}); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{
		Path: wsDir,
		Config: workspace.Config{
			Repos: map[string]workspace.RepoConfig{
				"myrepo": {Path: "repos/myrepo"},
			},
			Validate: map[string]string{
				"myrepo": "test -f valid.txt",
			},
		},
	}

	var buf bytes.Buffer
	if err := bisectAndRevert(ctx, ws, store, "myrepo", repoDir, "test -f valid.txt", initialHead, records, &buf); err != nil {
		t.Fatalf("bisectAndRevert: %v", err)
	}

	// Verify task2 was marked as failed.
	tasks, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var task2Found bool
	for _, tk := range tasks {
		if tk.ID == "task2" {
			task2Found = true
			if tk.Status != task.StatusFailed {
				t.Errorf("expected task2 status=failed, got %s", tk.Status)
			}
			if !strings.Contains(tk.Error, "end-of-run validation failed") {
				t.Errorf("expected error to mention end-of-run validation, got: %s", tk.Error)
			}
		}
	}
	if !task2Found {
		t.Error("task2 not found in store")
	}

	// Verify validation now passes after the revert.
	if err := runValidation(ctx, repoDir, "myrepo", ws.Config.Validate); err != nil {
		t.Errorf("validation should pass after revert: %v", err)
	}

	// Verify output mentions the revert and success.
	output := buf.String()
	if !strings.Contains(output, "Validation passes") {
		t.Errorf("expected output to mention passing validation, got: %s", output)
	}
}

func TestRunEndValidation_Passes(t *testing.T) {
	ctx := context.Background()

	// Set up workspace with a repo whose validation passes.
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repos", "myrepo")
	initTestRepoAt(t, repoDir)

	initialHead, err := runGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Task1: add a file.
	if err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "commit", "-m", "task1"); err != nil {
		t.Fatal(err)
	}
	commitA, err := runGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	records := []mergeRecord{
		{taskID: "task1", repo: "myrepo", commit: commitA},
	}

	tasksPath := filepath.Join(wsDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "task1", Status: task.StatusMerged, Repo: "myrepo"},
	}); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{
		Path: wsDir,
		Config: workspace.Config{
			Repos: map[string]workspace.RepoConfig{
				"myrepo": {Path: "repos/myrepo"},
			},
			Validate: map[string]string{
				"myrepo": "true", // always passes
			},
		},
	}

	var buf bytes.Buffer
	runEndValidation(ctx, ws, store, map[string]string{"myrepo": initialHead}, records, &buf)

	// Verify task1 is still merged (not reverted).
	tasks, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].Status != task.StatusMerged {
		t.Errorf("expected task1 to still be merged, got %s", tasks[0].Status)
	}
}

func TestRunEndValidation_FailsAndBisects(t *testing.T) {
	ctx := context.Background()

	// Set up workspace with a repo whose validation will fail.
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repos", "myrepo")
	initTestRepoAt(t, repoDir)

	// Add valid.txt so initial state passes validation.
	if err := os.WriteFile(filepath.Join(repoDir, "valid.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "commit", "-m", "add valid.txt"); err != nil {
		t.Fatal(err)
	}
	initialHead, err := runGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Task1 commit: remove valid.txt (breaks validation).
	if err := os.Remove(filepath.Join(repoDir, "valid.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "commit", "-m", "task1: break validation"); err != nil {
		t.Fatal(err)
	}
	commitA, err := runGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	records := []mergeRecord{
		{taskID: "task1", repo: "myrepo", commit: commitA},
	}

	tasksPath := filepath.Join(wsDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "task1", Status: task.StatusMerged, Repo: "myrepo"},
	}); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{
		Path: wsDir,
		Config: workspace.Config{
			Repos: map[string]workspace.RepoConfig{
				"myrepo": {Path: "repos/myrepo"},
			},
			Validate: map[string]string{
				"myrepo": "test -f valid.txt",
			},
		},
	}

	var buf bytes.Buffer
	runEndValidation(ctx, ws, store, map[string]string{"myrepo": initialHead}, records, &buf)

	// Verify task1 was marked as failed (reverted).
	tasks, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].Status != task.StatusFailed {
		t.Errorf("expected task1 status=failed, got %s", tasks[0].Status)
	}
	if !strings.Contains(tasks[0].Error, "end-of-run validation failed") {
		t.Errorf("expected error to mention end-of-run validation, got: %s", tasks[0].Error)
	}

	// Verify validation passes after the automatic revert.
	if err := runValidation(ctx, repoDir, "myrepo", ws.Config.Validate); err != nil {
		t.Errorf("validation should pass after revert: %v", err)
	}
}

func TestMatchCommitToTask(t *testing.T) {
	ctx := context.Background()

	// Set up a repo with 3 commits.
	dir := initTestRepo(t)

	// Commit A.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "commit", "-m", "commit A"); err != nil {
		t.Fatal(err)
	}
	commitA, _ := runGit(ctx, dir, "rev-parse", "HEAD")

	// Commit B.
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "commit", "-m", "commit B"); err != nil {
		t.Fatal(err)
	}
	commitB, _ := runGit(ctx, dir, "rev-parse", "HEAD")

	records := []mergeRecord{
		{taskID: "task1", repo: "myrepo", commit: commitA},
		{taskID: "task2", repo: "myrepo", commit: commitB},
	}

	// commitA should match task1.
	id, idx := matchCommitToTask(ctx, dir, commitA, records)
	if id != "task1" || idx != 0 {
		t.Errorf("expected task1/0, got %s/%d", id, idx)
	}

	// commitB should match task2.
	id, idx = matchCommitToTask(ctx, dir, commitB, records)
	if id != "task2" || idx != 1 {
		t.Errorf("expected task2/1, got %s/%d", id, idx)
	}

	// A non-existent commit should not match.
	id, idx = matchCommitToTask(ctx, dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", records)
	if id != "" || idx != -1 {
		t.Errorf("expected empty/-1, got %s/%d", id, idx)
	}
}

func TestPrintRunSummary_WithReverted(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")

	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "t1", Status: task.StatusFailed, Error: "end-of-run validation failed: this task's merge broke validation"},
		{ID: "t2", Status: task.StatusMerged},
	}); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{Path: dir}

	var buf bytes.Buffer
	printRunSummary(ws, store, &buf)

	output := buf.String()
	if !strings.Contains(output, "1 merged") {
		t.Errorf("expected '1 merged' in output, got: %s", output)
	}
	if !strings.Contains(output, "1 failed") {
		t.Errorf("expected '1 failed' in output, got: %s", output)
	}
	if !strings.Contains(output, "1 task(s) reverted by end-of-run validation") {
		t.Errorf("expected reverted count in output, got: %s", output)
	}
}
