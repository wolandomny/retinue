package workspace

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// ResolveGitHubToken resolves and caches the GitHub personal access token
// for the configured github_account. If no account is configured, it returns
// an empty string and nil error. The result is cached via sync.Once so
// subsequent calls do not shell out again.
func (w *Workspace) ResolveGitHubToken() (string, error) {
	account := w.Config.GithubAccount
	if account == "" {
		log.Printf("github: no github_account configured, inheriting parent credentials")
		return "", nil
	}

	w.ghOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "gh", "auth", "token", "--user", account)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			w.ghErr = fmt.Errorf("github: resolving token for account %q: %w\nstderr: %s",
				account, err, strings.TrimSpace(stderr.String()))
			return
		}

		w.ghToken = strings.TrimSpace(stdout.String())
		log.Printf("github: resolved token for account %q", account)
	})

	return w.ghToken, w.ghErr
}

// GitHubToken returns the cached GitHub token. It returns an empty string
// if the token has not yet been resolved or no account is configured.
func (w *Workspace) GitHubToken() string {
	return w.ghToken
}
