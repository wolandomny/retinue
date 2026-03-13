package workspace

import (
	"fmt"
	"sync"
	"testing"
)

func TestResolveGitHubToken_EmptyAccount(t *testing.T) {
	ws := &Workspace{
		Config: Config{GithubAccount: ""},
	}

	token, err := ws.ResolveGitHubToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "" {
		t.Errorf("token = %q, want empty", token)
	}
}

func TestGitHubToken_BeforeResolve(t *testing.T) {
	ws := &Workspace{
		Config: Config{GithubAccount: "some-user"},
	}

	token := ws.GitHubToken()
	if token != "" {
		t.Errorf("GitHubToken() before resolve = %q, want empty", token)
	}
}

func TestResolveGitHubToken_CachesResult(t *testing.T) {
	// Verify that sync.Once prevents multiple executions by pre-seeding
	// the cached token and marking the Once as done.
	ws := &Workspace{
		Config: Config{GithubAccount: "test-user"},
	}

	// Manually set the cached state to simulate a prior resolution.
	ws.ghToken = "cached-token-value"
	ws.ghOnce.Do(func() {
		// This function body will never execute because we call Do below,
		// but calling Do here "consumes" the Once so the real resolver
		// won't run.
	})

	token, err := ws.ResolveGitHubToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "cached-token-value" {
		t.Errorf("token = %q, want %q", token, "cached-token-value")
	}

	// GitHubToken getter should agree.
	if got := ws.GitHubToken(); got != "cached-token-value" {
		t.Errorf("GitHubToken() = %q, want %q", got, "cached-token-value")
	}
}

func TestResolveGitHubToken_CachesError(t *testing.T) {
	// Verify that a cached error is returned on subsequent calls.
	ws := &Workspace{
		Config: Config{GithubAccount: "bad-user"},
	}

	// Pre-seed an error via sync.Once.
	ws.ghErr = fmt.Errorf("github: resolving token for account %q: auth failed", "bad-user")
	ws.ghOnce.Do(func() {})

	token, err := ws.ResolveGitHubToken()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if token != "" {
		t.Errorf("token = %q, want empty on error", token)
	}
}

func TestResolveGitHubToken_OnceSemantics(t *testing.T) {
	// Ensure concurrent calls don't race.
	ws := &Workspace{
		Config: Config{GithubAccount: ""},
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = ws.ResolveGitHubToken()
		}()
	}
	wg.Wait()
}
