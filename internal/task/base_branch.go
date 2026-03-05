package task

import "github.com/wolandomny/retinue/internal/workspace"

const DefaultBaseBranch = "main"

// ResolveBaseBranch determines the base branch for a task using the
// resolution order: task override → repo config → default ("main").
func ResolveBaseBranch(t Task, repos map[string]workspace.RepoConfig) string {
	// 1. Task-level override.
	if t.BaseBranch != "" {
		return t.BaseBranch
	}

	// 2. Repo-level config.
	if t.Repo != "" {
		if rc, ok := repos[t.Repo]; ok && rc.BaseBranch != "" {
			return rc.BaseBranch
		}
	}

	// 3. Default.
	return DefaultBaseBranch
}
