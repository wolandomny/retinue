package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
)

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

	ws := &workspace.Workspace{Path: dir}

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

func TestPrintRunSummary_NoArchive(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")

	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "t1", Status: task.StatusMerged},
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
	archiveStore := task.NewFileStore(archivePath)
	archived, err := archiveStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 2 {
		t.Fatalf("expected 2 archived tasks, got %d", len(archived))
	}
}

func TestMarkTaskMergedNoArchive(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "t1", Status: task.StatusDone},
	})

	markTaskMergedNoArchive(store, "t1")

	tasks, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task (not archived), got %d", len(tasks))
	}
	if tasks[0].Status != task.StatusMerged {
		t.Fatalf("expected merged, got %s", tasks[0].Status)
	}
	if tasks[0].FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
}

func TestBuildDependencyContext(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "dep1", Status: task.StatusMerged, Description: "Setup DB", Result: "Created schema"},
		{ID: "dep2", Status: task.StatusMerged, Description: "Add auth", Result: "Added JWT auth"},
		{ID: "main-task", Status: task.StatusPending, DependsOn: []string{"dep1", "dep2"}},
	})

	mainTask := task.Task{
		ID:        "main-task",
		DependsOn: []string{"dep1", "dep2"},
	}

	ctx := buildDependencyContext(store, mainTask)

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

func TestBuildDependencyContext_NoDeps(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "t1", Status: task.StatusPending},
	})

	ctx := buildDependencyContext(store, task.Task{ID: "t1"})
	if ctx != "" {
		t.Errorf("expected empty context for task with no deps, got: %s", ctx)
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
