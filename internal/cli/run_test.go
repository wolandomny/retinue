package cli

import (
	"bytes"
	"fmt"
	"io"
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
