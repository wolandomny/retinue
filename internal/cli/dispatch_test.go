package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wolandomny/retinue/internal/agent"
	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
)

func hasEnv(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

func TestResolveWorkerLaunch_DowngradesUltracodeWhenDisabled(t *testing.T) {
	ws := &workspace.Workspace{
		Config: workspace.Config{AllowUltracode: false},
	}
	tk := &task.Task{ID: "t-ultra", Effort: "ultracode"}

	var effort string
	var env []string
	// The downgrade emits an operator-facing warning to stderr; capture it so we
	// assert the warning is actually surfaced (not silently downgraded).
	stderr := captureStderr(t, func() {
		effort, env = resolveWorkerLaunch(tk, ws)
	})

	if effort != "xhigh" {
		t.Errorf("effort = %q, want %q (downgraded)", effort, "xhigh")
	}
	// Downgraded workers are non-ultracode, so workflows must be disabled.
	if !hasEnv(env, "CLAUDE_CODE_DISABLE_WORKFLOWS=1") {
		t.Errorf("expected CLAUDE_CODE_DISABLE_WORKFLOWS=1 in env, got %v", env)
	}
	// The warning must be emitted and must identify both the offending task and
	// the new effort level, so operators can see why ultracode was refused.
	if !strings.Contains(stderr, "downgrading to xhigh") {
		t.Errorf("expected 'downgrading to xhigh' warning on stderr, got: %q", stderr)
	}
	if !strings.Contains(stderr, "t-ultra") {
		t.Errorf("expected task ID %q in downgrade warning, got: %q", "t-ultra", stderr)
	}
	if !strings.Contains(stderr, "allow_ultracode") {
		t.Errorf("expected warning to mention allow_ultracode, got: %q", stderr)
	}
}

// TestResolveWorkerLaunch_NoWarningWhenAllowed guards against a spurious
// downgrade warning when the workspace has opted into ultracode.
func TestResolveWorkerLaunch_NoWarningWhenAllowed(t *testing.T) {
	ws := &workspace.Workspace{
		Config: workspace.Config{AllowUltracode: true},
	}
	tk := &task.Task{ID: "t-ultra", Effort: "ultracode"}

	stderr := captureStderr(t, func() {
		resolveWorkerLaunch(tk, ws)
	})
	if strings.Contains(stderr, "downgrading") {
		t.Errorf("did not expect a downgrade warning when allow_ultracode is true, got: %q", stderr)
	}
}

func TestResolveWorkerLaunch_KeepsUltracodeWhenAllowed(t *testing.T) {
	ws := &workspace.Workspace{
		Config: workspace.Config{AllowUltracode: true},
	}
	tk := &task.Task{ID: "t-ultra", Effort: "ultracode"}

	effort, env := resolveWorkerLaunch(tk, ws)

	if effort != "ultracode" {
		t.Errorf("effort = %q, want %q", effort, "ultracode")
	}
	// Ultracode workers keep workflows enabled.
	if hasEnv(env, "CLAUDE_CODE_DISABLE_WORKFLOWS=1") {
		t.Errorf("did not expect CLAUDE_CODE_DISABLE_WORKFLOWS=1 for ultracode worker, got %v", env)
	}
}

func TestResolveWorkerLaunch_DisablesWorkflowsForPlainWorker(t *testing.T) {
	ws := &workspace.Workspace{
		Config: workspace.Config{Effort: "high"},
	}
	tk := &task.Task{ID: "t-plain"}

	effort, env := resolveWorkerLaunch(tk, ws)

	if effort != "high" {
		t.Errorf("effort = %q, want %q", effort, "high")
	}
	if !hasEnv(env, "CLAUDE_CODE_DISABLE_WORKFLOWS=1") {
		t.Errorf("expected CLAUDE_CODE_DISABLE_WORKFLOWS=1 in env, got %v", env)
	}
}

func TestResolveWorkDir_NoRepo(t *testing.T) {
	ws := &workspace.Workspace{
		Path: "/tmp/test-apartment",
		Config: workspace.Config{
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: "repos/myrepo"}},
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
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: "repos/myrepo"}},
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

	// Create a git repo to act as the source repo using the test helper.
	repoDir := filepath.Join(aptDir, "repos", "myrepo")
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create the actual repo using the helper (which properly sets up 'main' branch).
	tempRepo := initTestRepo(t)
	if err := os.Rename(tempRepo, repoDir); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: "repos/myrepo"}},
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

	// Create a git repo to act as the source repo using the test helper.
	repoDir := filepath.Join(aptDir, "repos", "myrepo")
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create the actual repo using the helper (which properly sets up 'main' branch).
	tempRepo := initTestRepo(t)
	if err := os.Rename(tempRepo, repoDir); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: "repos/myrepo"}},
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
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: repoRelPath}},
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
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: "repos/myrepo"}},
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

func TestCommitStylePrompt_Empty(t *testing.T) {
	got := commitStylePrompt("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestCommitStylePrompt_Conventional(t *testing.T) {
	got := commitStylePrompt("conventional")
	if got == "" {
		t.Fatal("expected non-empty prompt for 'conventional'")
	}
	if !strings.Contains(got, "feat:") {
		t.Error("expected 'feat:' in conventional prompt")
	}
	if !strings.Contains(got, "fix:") {
		t.Error("expected 'fix:' in conventional prompt")
	}
	if !strings.Contains(got, "Conventional Commits") {
		t.Error("expected 'Conventional Commits' in prompt")
	}
}

func TestCommitStylePrompt_Custom(t *testing.T) {
	got := commitStylePrompt("Always prefix with JIRA ticket number")
	if !strings.Contains(got, "JIRA ticket number") {
		t.Errorf("expected custom string in prompt, got %q", got)
	}
}

func TestBuildDependencyContext_WithResults(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "dep-1", Description: "First dep", Status: task.StatusDone, Result: "Result from dep 1"},
		{ID: "dep-2", Description: "Second dep", Status: task.StatusDone, Result: "Result from dep 2"},
	})
	got := buildDependencyContext(store, []string{"dep-1", "dep-2"})
	if !strings.Contains(got, "Context from Completed Dependencies") {
		t.Error("missing header")
	}
	if !strings.Contains(got, "dep-1") || !strings.Contains(got, "Result from dep 1") {
		t.Error("missing dep-1 content")
	}
	if !strings.Contains(got, "dep-2") || !strings.Contains(got, "Result from dep 2") {
		t.Error("missing dep-2 content")
	}
}

func TestBuildDependencyContext_Truncation(t *testing.T) {
	// Create a result longer than 4000 chars.
	longResult := strings.Repeat("x", 5000) + "TAIL_MARKER"
	store := writeTasks(t, []task.Task{
		{ID: "dep-long", Description: "Long dep", Status: task.StatusDone, Result: longResult},
	})
	got := buildDependencyContext(store, []string{"dep-long"})
	if !strings.Contains(got, "TAIL_MARKER") {
		t.Error("truncation should keep the tail (TAIL_MARKER missing)")
	}
	// The 'x' prefix should be partially truncated.
	xCount := strings.Count(got, "x")
	if xCount >= 5000 {
		t.Errorf("expected truncation, but got %d x's", xCount)
	}
}

func TestBuildDependencyContext_NoDeps(t *testing.T) {
	store := writeTasks(t, []task.Task{})
	got := buildDependencyContext(store, []string{})
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestBuildDependencyContext_EmptyResult(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "dep-empty", Description: "Empty dep", Status: task.StatusDone, Result: ""},
	})
	got := buildDependencyContext(store, []string{"dep-empty"})
	// Dep with empty result still appears (with description), just without a Result line.
	if !strings.Contains(got, "dep-empty") {
		t.Errorf("expected dep-empty in context, got %q", got)
	}
	if strings.Contains(got, "Result:") {
		t.Errorf("expected no Result line for empty result, got %q", got)
	}
}

// --- Dispatch concurrency / governance tests -------------------------------
//
// These tests exercise dispatchAll's ultracode semaphore and the per-worker
// disallowed-tools wiring WITHOUT spawning real `claude` processes. They rely
// on the injectable runner seam: dispatchAll/dispatchOne accept an
// agent.Runner; when nil they build the real tmux runner (production), and
// tests pass a fakeRunner instead.
//
// What is covered:
//   - The ultracode semaphore never lets more than MaxUltracodeWorkers
//     ultracode workers run concurrently (the SAFETY property).
//   - Non-ultracode workers are NOT gated by the ultracode semaphore.
//   - The acquire order (ultraSem before sem) does not deadlock under
//     saturation; the scheduler always drains.
//   - The "AskUserQuestion" deny actually reaches the worker invocation.
//
// What is NOT covered here: the real tmux/claude launch path (that lives in
// internal/agent) and worktree creation (covered by the resolveWorkDir tests).
// The fakeRunner returns an error so dispatchOne takes its failure path, which
// avoids touching tmux at all; failed tasks are not "ready", so the scheduler
// terminates cleanly.

// fakeRunner is an agent.Runner that records concurrency without launching any
// real process. It separately tracks ultracode vs non-ultracode in-flight
// counts (distinguished by RunOpts.Effort) and the peak of each, so tests can
// assert the ultracode cap is honored while plain workers are not throttled.
type fakeRunner struct {
	delay time.Duration

	mu          sync.Mutex
	ultraCur    int
	ultraPeak   int
	otherCur    int
	otherPeak   int
	runCount   int
	disallowed []string // RunOpts.DisallowedTools seen, one entry per Run call
	efforts    []string // RunOpts.Effort seen, one entry per Run call
}

func (r *fakeRunner) Run(_ context.Context, opts agent.RunOpts) (agent.Result, error) {
	isUltra := opts.Effort == "ultracode"

	r.mu.Lock()
	r.runCount++
	r.disallowed = append(r.disallowed, opts.DisallowedTools)
	r.efforts = append(r.efforts, opts.Effort)
	if isUltra {
		r.ultraCur++
		if r.ultraCur > r.ultraPeak {
			r.ultraPeak = r.ultraCur
		}
	} else {
		r.otherCur++
		if r.otherCur > r.otherPeak {
			r.otherPeak = r.otherCur
		}
	}
	r.mu.Unlock()

	// Hold the slot long enough that concurrently-launched workers overlap,
	// making the observed peak deterministic relative to the semaphore cap.
	if r.delay > 0 {
		time.Sleep(r.delay)
	}

	r.mu.Lock()
	if isUltra {
		r.ultraCur--
	} else {
		r.otherCur--
	}
	r.mu.Unlock()

	// Return an error so dispatchOne takes the failure path: it records the task
	// as failed and, crucially, does NOT call the real tmux KillWindow. Failed
	// tasks are not re-dispatched, so the scheduler converges.
	return agent.Result{}, fmt.Errorf("fakeRunner: simulated failure")
}

func (r *fakeRunner) snapshot() (ultraPeak, otherPeak, runCount int, disallowed, efforts []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ultraPeak, r.otherPeak, r.runCount,
		append([]string(nil), r.disallowed...),
		append([]string(nil), r.efforts...)
}

// newDispatchTestWorkspace builds a workspace backed by a temp dir plus a
// FileStore pre-populated with the given tasks. The tasks have no Repo, so
// resolveWorkDir returns the workspace path and no git/worktree work occurs.
func newDispatchTestWorkspace(t *testing.T, cfg workspace.Config, tasks []task.Task) (*workspace.Workspace, *task.FileStore) {
	t.Helper()
	if cfg.Name == "" {
		cfg.Name = "test"
	}
	ws := &workspace.Workspace{Path: t.TempDir(), Config: cfg}
	store := task.NewFileStore(ws.TasksPath())
	if err := store.Save(tasks); err != nil {
		t.Fatalf("saving tasks: %v", err)
	}
	return ws, store
}

// makePendingTasks builds n ready (pending, no-dep, no-repo) tasks with the
// given effort and an id prefix.
func makePendingTasks(prefix, effort string, n int) []task.Task {
	tasks := make([]task.Task, n)
	for i := 0; i < n; i++ {
		tasks[i] = task.Task{
			ID:     fmt.Sprintf("%s-%d", prefix, i),
			Status: task.StatusPending,
			Effort: effort,
			Prompt: "do work",
		}
	}
	return tasks
}

// runDispatchAllWithin runs dispatchAll in a goroutine and fails the test if it
// does not return within timeout. A real deadlock in the semaphore-acquire
// path would hang regardless of context cancellation, so we use a wall-clock
// guard rather than a context deadline.
func runDispatchAllWithin(t *testing.T, ws *workspace.Workspace, store *task.FileStore, runner agent.Runner, timeout time.Duration) {
	t.Helper()
	errc := make(chan error, 1)
	go func() {
		errc <- dispatchAll(context.Background(), ws, store, io.Discard, runner)
	}()
	select {
	case <-errc:
		// dispatchAll itself returns nil even when individual tasks fail; we
		// don't assert on it here (the fakeRunner always "fails" tasks).
	case <-time.After(timeout):
		t.Fatalf("dispatchAll did not complete within %s — possible deadlock in the ultracode/worker semaphore acquire order", timeout)
	}
}

// TestDispatchAll_UltracodeSemaphoreCapsConcurrency is the highest-priority
// guardrail test: with many ultracode tasks ready at once, no more than
// MaxUltracodeWorkers may run concurrently. A regression that dropped the
// ultraSem gating would let the peak climb toward the task count (or MaxWorkers).
func TestDispatchAll_UltracodeSemaphoreCapsConcurrency(t *testing.T) {
	const ultraCap = 2
	ws, store := newDispatchTestWorkspace(t, workspace.Config{
		AllowUltracode:      true,
		MaxUltracodeWorkers: ultraCap,
		MaxWorkers:          10, // deliberately loose so ultraSem is the binding constraint
	}, makePendingTasks("ultra", "ultracode", 6))

	fr := &fakeRunner{delay: 60 * time.Millisecond}
	runDispatchAllWithin(t, ws, store, fr, 15*time.Second)

	ultraPeak, _, runCount, _, efforts := fr.snapshot()

	if runCount != 6 {
		t.Fatalf("expected all 6 ultracode tasks to run, got runCount=%d", runCount)
	}
	for _, e := range efforts {
		if e != "ultracode" {
			t.Fatalf("expected every worker to launch with effort=ultracode, saw %q (efforts=%v)", e, efforts)
		}
	}
	// SAFETY property: never exceed the cap.
	if ultraPeak > ultraCap {
		t.Errorf("ultracode concurrency peak = %d, exceeds MaxUltracodeWorkers = %d", ultraPeak, ultraCap)
	}
	// Meaningfulness: the gate should still allow up to the cap, otherwise the
	// test would pass even if dispatch serialized everything (hiding a real cap
	// regression behind over-serialization).
	if ultraPeak != ultraCap {
		t.Errorf("ultracode concurrency peak = %d, want exactly %d (gate should saturate to the cap)", ultraPeak, ultraCap)
	}
}

// TestDispatchAll_NonUltracodeNotThrottledByUltraSem confirms plain workers do
// not consume the ultracode semaphore: with the ultracode cap pinned to 1, a
// batch of non-ultracode tasks must still run concurrently up to MaxWorkers.
func TestDispatchAll_NonUltracodeNotThrottledByUltraSem(t *testing.T) {
	const ultraCap = 1
	const workers = 8
	const n = 6
	ws, store := newDispatchTestWorkspace(t, workspace.Config{
		AllowUltracode:      true,
		MaxUltracodeWorkers: ultraCap,
		MaxWorkers:          workers,
	}, makePendingTasks("plain", "high", n))

	fr := &fakeRunner{delay: 60 * time.Millisecond}
	runDispatchAllWithin(t, ws, store, fr, 15*time.Second)

	ultraPeak, otherPeak, runCount, _, _ := fr.snapshot()

	if runCount != n {
		t.Fatalf("expected all %d tasks to run, got runCount=%d", n, runCount)
	}
	if ultraPeak != 0 {
		t.Errorf("non-ultracode tasks should never register as ultracode, ultraPeak=%d", ultraPeak)
	}
	// If plain workers were (incorrectly) gated by the size-1 ultracode
	// semaphore, otherPeak would be 1. It must climb well past the ultracode cap.
	if otherPeak <= ultraCap {
		t.Errorf("non-ultracode peak = %d, want > %d (plain workers must not be throttled by ultraSem)", otherPeak, ultraCap)
	}
}

// TestDispatchAll_MixedSaturationNoDeadlock saturates BOTH semaphores with a
// mix of ultracode and plain tasks under a tight worker cap and asserts the
// scheduler drains within a timeout. This guards the acquire/release ordering
// (ultraSem before sem, released in reverse) against a deadlock regression.
func TestDispatchAll_MixedSaturationNoDeadlock(t *testing.T) {
	const ultraCap = 2
	const workers = 2 // tight: ultra acquisition must interleave with the worker cap
	ultra := makePendingTasks("ultra", "ultracode", 4)
	plain := makePendingTasks("plain", "high", 4)

	ws, store := newDispatchTestWorkspace(t, workspace.Config{
		AllowUltracode:      true,
		MaxUltracodeWorkers: ultraCap,
		MaxWorkers:          workers,
	}, append(ultra, plain...))

	fr := &fakeRunner{delay: 20 * time.Millisecond}
	runDispatchAllWithin(t, ws, store, fr, 20*time.Second)

	ultraPeak, _, runCount, _, _ := fr.snapshot()

	if runCount != 8 {
		t.Fatalf("expected all 8 tasks to run, got runCount=%d", runCount)
	}
	if ultraPeak > ultraCap {
		t.Errorf("ultracode concurrency peak = %d, exceeds cap = %d", ultraPeak, ultraCap)
	}
}

// TestDispatchAll_DrainsBacklogLargerThanWorkerCap is a regression guard for a
// scheduler deadlock: when far more tasks are ready than MaxWorkers, the launch
// loop must still drain. The bug was that each worker sent its completion pulse
// on the `done` channel (buffered at MaxWorkers) BEFORE releasing its worker
// slot; once the buffer filled, in-flight workers pinned every slot while the
// launch loop blocked acquiring one — a hang. The fix releases the slot before
// the pulse. This test uses a deliberately tight cap and a large backlog so a
// regression re-deadlocks and trips the timeout.
//
// Note: this path is independent of ultracode — it exercises the general
// worker semaphore — but it is part of the same dispatch safety surface.
func TestDispatchAll_DrainsBacklogLargerThanWorkerCap(t *testing.T) {
	const workers = 2
	const n = 16 // well past the backlog threshold (~2*workers) where the old code hung
	ws, store := newDispatchTestWorkspace(t, workspace.Config{
		MaxWorkers: workers,
	}, makePendingTasks("plain", "high", n))

	fr := &fakeRunner{delay: 5 * time.Millisecond}
	runDispatchAllWithin(t, ws, store, fr, 20*time.Second)

	_, otherPeak, runCount, _, _ := fr.snapshot()

	if runCount != n {
		t.Fatalf("expected all %d tasks to run, got runCount=%d (scheduler likely stalled)", n, runCount)
	}
	if otherPeak > workers {
		t.Errorf("worker concurrency peak = %d, exceeds MaxWorkers = %d", otherPeak, workers)
	}
}

// TestDispatchOne_PassesDisallowedTools asserts the AskUserQuestion deny is
// wired all the way through to the worker invocation (RunOpts.DisallowedTools),
// not merely validated at the runner's arg-building unit test. Workers must not
// be able to block on interactive prompts.
func TestDispatchOne_PassesDisallowedTools(t *testing.T) {
	ws, store := newDispatchTestWorkspace(t, workspace.Config{Name: "test"},
		[]task.Task{{ID: "deny-1", Status: task.StatusPending, Prompt: "do work"}})

	tk := task.Task{ID: "deny-1", Status: task.StatusPending, Prompt: "do work"}
	fr := &fakeRunner{}

	// dispatchOne returns an error because the fakeRunner reports failure; that
	// is expected and irrelevant — we only care about what reached the runner.
	_ = dispatchOne(context.Background(), ws, store, &tk, io.Discard, fr)

	_, _, runCount, disallowed, _ := fr.snapshot()
	if runCount != 1 {
		t.Fatalf("expected the worker to be invoked exactly once, got runCount=%d", runCount)
	}
	if len(disallowed) != 1 || disallowed[0] != "AskUserQuestion" {
		t.Errorf("expected DisallowedTools=%q to reach the worker invocation, got %v", "AskUserQuestion", disallowed)
	}
}

// emptyOutputRunner is an agent.Runner that simulates a fast/empty worker
// failure: it reports SUCCESS (err == nil) but produces no output. dispatchOne
// must treat this as a failure rather than a phantom success.
type emptyOutputRunner struct{}

func (emptyOutputRunner) Run(_ context.Context, _ agent.RunOpts) (agent.Result, error) {
	return agent.Result{Output: ""}, nil
}

// TestDispatchOne_EmptyOutputIsRecordedFailed guards the empty-output path: a
// runner returning success with empty output must be recorded StatusFailed (and
// dispatchOne must return an error), never marked done.
func TestDispatchOne_EmptyOutputIsRecordedFailed(t *testing.T) {
	ws, store := newDispatchTestWorkspace(t, workspace.Config{Name: "test"},
		[]task.Task{{ID: "ff-1", Status: task.StatusPending, Prompt: "do work"}})
	tk := task.Task{ID: "ff-1", Status: task.StatusPending, Prompt: "do work"}
	if err := dispatchOne(context.Background(), ws, store, &tk, io.Discard, emptyOutputRunner{}); err == nil {
		t.Fatal("expected error for empty-output worker")
	}
	got, _ := store.Get("ff-1")
	if got.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed (empty output must not be a phantom success)", got.Status)
	}
}

// --- Commit-aware status decision (incidents 1 & 2) ------------------------
//
// For a task WITH a repo, dispatchOne must base done/failed on whether the task
// branch has commits beyond its base, NOT on the worker's result event:
//   - branch HAS commits  -> done (recover real work even with no result text).
//   - branch has NO commits -> failed regardless of output (no phantom merge).
// These tests drive the full dispatchOne against a real throwaway git repo so
// the production worktree path runs; the injected runner operates on the actual
// worktree (opts.WorkDir) to simulate a worker that does or does not commit.

// repoWorkRunner is an agent.Runner that, when invoked, optionally creates a
// commit in the worker's worktree (opts.WorkDir) to simulate committed work,
// then returns the configured output with err == nil (a "clean" worker exit).
type repoWorkRunner struct {
	commit bool
	output string
}

func (r repoWorkRunner) Run(_ context.Context, opts agent.RunOpts) (agent.Result, error) {
	if r.commit {
		ctx := context.Background()
		fname := filepath.Join(opts.WorkDir, "worker-change.txt")
		if err := os.WriteFile(fname, []byte("work product\n"), 0o644); err != nil {
			return agent.Result{}, err
		}
		if _, err := runGit(ctx, opts.WorkDir, "add", "."); err != nil {
			return agent.Result{}, err
		}
		if _, err := runGit(ctx, opts.WorkDir, "commit", "-m", "worker commit"); err != nil {
			return agent.Result{}, err
		}
	}
	return agent.Result{Output: r.output}, nil
}

// newRepoDispatchWorkspace builds a workspace with a real git repo at
// repos/myrepo (base branch "main", one commit) plus a FileStore holding a
// single pending repo task. dispatchOne will create the worktree on the
// production path.
func newRepoDispatchWorkspace(t *testing.T, taskID string) (*workspace.Workspace, *task.FileStore) {
	t.Helper()
	aptDir := t.TempDir()
	repoDir := filepath.Join(aptDir, "repos", "myrepo")
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(initTestRepo(t), repoDir); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Name:  "test",
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: "repos/myrepo"}},
		},
	}
	store := task.NewFileStore(ws.TasksPath())
	if err := store.Save([]task.Task{
		{ID: taskID, Repo: "myrepo", Status: task.StatusPending, Prompt: "do work"},
	}); err != nil {
		t.Fatalf("saving tasks: %v", err)
	}
	return ws, store
}

// TestDispatchOne_RepoCommitsRecoverDoneWithEmptyOutput covers INCIDENT 1: the
// worker committed real work but its result event never flushed (empty output).
// Commit presence must mark the task done, recovering the work, and synthesize
// a result note.
func TestDispatchOne_RepoCommitsRecoverDoneWithEmptyOutput(t *testing.T) {
	ws, store := newRepoDispatchWorkspace(t, "inc1")
	tk := task.Task{ID: "inc1", Repo: "myrepo", Status: task.StatusPending, Prompt: "do work"}

	err := dispatchOne(context.Background(), ws, store, &tk, io.Discard, repoWorkRunner{commit: true, output: ""})
	if err != nil {
		t.Fatalf("expected success when branch has commits, got error: %v", err)
	}
	got, _ := store.Get("inc1")
	if got.Status != task.StatusDone {
		t.Errorf("status = %q, want done (committed work must be recovered)", got.Status)
	}
	if !strings.Contains(got.Result, "recovered") || !strings.Contains(got.Result, "commit") {
		t.Errorf("expected synthesized recovery note in Result, got %q", got.Result)
	}
}

// TestDispatchOne_RepoNoCommitsErrorOutputFailed covers INCIDENT 2: the worker
// committed nothing but emitted output (e.g. an error string). It must be
// failed, never done — no phantom branch may be merged.
func TestDispatchOne_RepoNoCommitsErrorOutputFailed(t *testing.T) {
	ws, store := newRepoDispatchWorkspace(t, "inc2")
	tk := task.Task{ID: "inc2", Repo: "myrepo", Status: task.StatusPending, Prompt: "do work"}

	errText := "API Error: 529 overloaded"
	err := dispatchOne(context.Background(), ws, store, &tk, io.Discard, repoWorkRunner{commit: false, output: errText})
	if err == nil {
		t.Fatal("expected error when branch has no commits (phantom must not succeed)")
	}
	got, _ := store.Get("inc2")
	if got.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed (no commits => never a phantom success)", got.Status)
	}
	// The worker's output is preserved for inspection.
	if got.Result != errText {
		t.Errorf("expected worker output %q preserved in Result, got %q", errText, got.Result)
	}
}

// TestDispatchOne_RepoNoCommitsEmptyOutputFailed is the phantom case: no commits
// and no output. Must be failed (preserving the existing invariant).
func TestDispatchOne_RepoNoCommitsEmptyOutputFailed(t *testing.T) {
	ws, store := newRepoDispatchWorkspace(t, "phantom")
	tk := task.Task{ID: "phantom", Repo: "myrepo", Status: task.StatusPending, Prompt: "do work"}

	err := dispatchOne(context.Background(), ws, store, &tk, io.Discard, repoWorkRunner{commit: false, output: ""})
	if err == nil {
		t.Fatal("expected error for phantom run (no commits, no output)")
	}
	got, _ := store.Get("phantom")
	if got.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed (phantom must never be merged)", got.Status)
	}
}

// TestDispatchOne_RepoCommitsWithResultTextDone is the normal success path: the
// worker committed AND produced result text. The task is done and keeps that
// text (the synthesized note is only used when the result event is missing).
func TestDispatchOne_RepoCommitsWithResultTextDone(t *testing.T) {
	ws, store := newRepoDispatchWorkspace(t, "happy")
	tk := task.Task{ID: "happy", Repo: "myrepo", Status: task.StatusPending, Prompt: "do work"}

	resultText := "Implemented the feature and committed."
	err := dispatchOne(context.Background(), ws, store, &tk, io.Discard, repoWorkRunner{commit: true, output: resultText})
	if err != nil {
		t.Fatalf("expected success on normal commit+result, got error: %v", err)
	}
	got, _ := store.Get("happy")
	if got.Status != task.StatusDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if got.Result != resultText {
		t.Errorf("expected Result %q, got %q", resultText, got.Result)
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns whatever
// was written. Used to assert operator-facing warnings are actually emitted.
// Not safe for concurrent use; callers must not run in parallel.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()
	os.Stderr = old
	out := <-done
	_ = r.Close()
	return out
}
