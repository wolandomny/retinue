package session_test

import (
	"context"
	"sort"
	"testing"

	"github.com/wolandomny/retinue/internal/session"
)

func TestFakeManager_CreateAndExists(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	exists, err := mgr.Exists(ctx, "mySession")
	if err != nil {
		t.Fatalf("Exists before Create: unexpected error: %v", err)
	}
	if exists {
		t.Fatal("Exists before Create: expected false, got true")
	}

	if err := mgr.Create(ctx, "mySession", "/tmp", "echo hello"); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	exists, err = mgr.Exists(ctx, "mySession")
	if err != nil {
		t.Fatalf("Exists after Create: unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("Exists after Create: expected true, got false")
	}
}

func TestFakeManager_CreateDuplicate(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	if err := mgr.Create(ctx, "dup", "/tmp", "sleep 1"); err != nil {
		t.Fatalf("first Create: unexpected error: %v", err)
	}
	if err := mgr.Create(ctx, "dup", "/tmp", "sleep 1"); err == nil {
		t.Fatal("second Create with same name: expected error, got nil")
	}
}

func TestFakeManager_Kill(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	if err := mgr.Create(ctx, "toKill", "/tmp", "sleep 10"); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	if err := mgr.Kill(ctx, "toKill"); err != nil {
		t.Fatalf("Kill existing session: unexpected error: %v", err)
	}

	exists, err := mgr.Exists(ctx, "toKill")
	if err != nil {
		t.Fatalf("Exists after Kill: unexpected error: %v", err)
	}
	if exists {
		t.Fatal("Exists after Kill: expected false, got true")
	}
}

func TestFakeManager_KillNonExistent(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// Killing a session that does not exist must not return an error.
	if err := mgr.Kill(ctx, "ghost"); err != nil {
		t.Fatalf("Kill non-existent session: expected nil, got: %v", err)
	}
}

func TestFakeManager_Wait(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	if err := mgr.Create(ctx, "waiter", "/tmp", "sleep 1"); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	// Wait should return immediately for the fake.
	if err := mgr.Wait(ctx, "waiter"); err != nil {
		t.Fatalf("Wait: unexpected error: %v", err)
	}
}

func TestTmuxArgs_EmptySocket(t *testing.T) {
	mgr := session.NewTmuxManager("")
	got := mgr.TmuxArgs("new-session", "-d", "-s", "test")
	expected := []string{"new-session", "-d", "-s", "test"}
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("arg %d: expected %q, got %q", i, expected[i], got[i])
		}
	}
}

func TestTmuxArgs_WithSocket(t *testing.T) {
	mgr := session.NewTmuxManager("my-socket")
	got := mgr.TmuxArgs("new-session", "-d", "-s", "test")
	expected := []string{"-L", "my-socket", "new-session", "-d", "-s", "test"}
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("arg %d: expected %q, got %q", i, expected[i], got[i])
		}
	}
}

func TestFakeManager_CreateWindowAndHasWindow(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	if err := mgr.CreateWindow(ctx, "sess1", "win1", "/tmp", "echo hi"); err != nil {
		t.Fatalf("CreateWindow: unexpected error: %v", err)
	}

	has, err := mgr.HasWindow(ctx, "sess1", "win1")
	if err != nil {
		t.Fatalf("HasWindow: unexpected error: %v", err)
	}
	if !has {
		t.Fatal("HasWindow: expected true, got false")
	}

	has, err = mgr.HasWindow(ctx, "sess1", "win-nope")
	if err != nil {
		t.Fatalf("HasWindow non-existent: unexpected error: %v", err)
	}
	if has {
		t.Fatal("HasWindow non-existent: expected false, got true")
	}
}

func TestFakeManager_CreateWindowDuplicate(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	if err := mgr.CreateWindow(ctx, "sess1", "win1", "/tmp", "echo hi"); err != nil {
		t.Fatalf("first CreateWindow: unexpected error: %v", err)
	}
	if err := mgr.CreateWindow(ctx, "sess1", "win1", "/tmp", "echo hi"); err == nil {
		t.Fatal("second CreateWindow with same name: expected error, got nil")
	}
}

func TestFakeManager_CreateWindowCreatesSession(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// First CreateWindow on a new session should succeed (session implicitly created).
	if err := mgr.CreateWindow(ctx, "newsess", "win1", "/tmp", "echo hello"); err != nil {
		t.Fatalf("CreateWindow on new session: unexpected error: %v", err)
	}

	has, err := mgr.HasWindow(ctx, "newsess", "win1")
	if err != nil {
		t.Fatalf("HasWindow: unexpected error: %v", err)
	}
	if !has {
		t.Fatal("HasWindow: expected true after CreateWindow on new session")
	}
}

func TestFakeManager_KillWindow(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	if err := mgr.CreateWindow(ctx, "sess1", "win1", "/tmp", "echo hi"); err != nil {
		t.Fatalf("CreateWindow: unexpected error: %v", err)
	}

	if err := mgr.KillWindow(ctx, "sess1", "win1"); err != nil {
		t.Fatalf("KillWindow: unexpected error: %v", err)
	}

	has, err := mgr.HasWindow(ctx, "sess1", "win1")
	if err != nil {
		t.Fatalf("HasWindow after KillWindow: unexpected error: %v", err)
	}
	if has {
		t.Fatal("HasWindow after KillWindow: expected false, got true")
	}
}

func TestFakeManager_KillWindowNonExistent(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// Killing a window that does not exist must not return an error.
	if err := mgr.KillWindow(ctx, "nosess", "nowin"); err != nil {
		t.Fatalf("KillWindow non-existent: expected nil, got: %v", err)
	}
}

func TestFakeManager_ListWindows(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	if err := mgr.CreateWindow(ctx, "sess1", "alpha", "/tmp", "cmd1"); err != nil {
		t.Fatalf("CreateWindow alpha: unexpected error: %v", err)
	}
	if err := mgr.CreateWindow(ctx, "sess1", "beta", "/tmp", "cmd2"); err != nil {
		t.Fatalf("CreateWindow beta: unexpected error: %v", err)
	}
	if err := mgr.CreateWindow(ctx, "sess1", "gamma", "/tmp", "cmd3"); err != nil {
		t.Fatalf("CreateWindow gamma: unexpected error: %v", err)
	}

	names, err := mgr.ListWindows(ctx, "sess1")
	if err != nil {
		t.Fatalf("ListWindows: unexpected error: %v", err)
	}

	sort.Strings(names)
	expected := []string{"alpha", "beta", "gamma"}
	if len(names) != len(expected) {
		t.Fatalf("ListWindows: expected %v, got %v", expected, names)
	}
	for i := range expected {
		if names[i] != expected[i] {
			t.Fatalf("ListWindows[%d]: expected %q, got %q", i, expected[i], names[i])
		}
	}
}

func TestFakeManager_ListWindowsNoSession(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	names, err := mgr.ListWindows(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("ListWindows: unexpected error: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("ListWindows: expected empty slice, got %v", names)
	}
}

// Ensure FakeManager satisfies the Manager interface at compile time.
var _ session.Manager = (*session.FakeManager)(nil)
