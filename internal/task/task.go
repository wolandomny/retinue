// Package task defines the task model and persistence layer for
// retinue's task DAG.
package task

import "time"

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusMerged     = "merged"
	StatusFailed     = "failed"
)

// Task represents a unit of work in the retinue task DAG. Each task
// has a unique ID, a status lifecycle, and optional dependencies on
// other tasks.
type Task struct {
	ID          string            `yaml:"id"`
	Description string            `yaml:"description"`
	Repo        string            `yaml:"repo"`
	Branch      string            `yaml:"branch,omitempty"`
	BaseBranch  string            `yaml:"base_branch,omitempty"`
	DependsOn   []string          `yaml:"depends_on"`
	Status      string            `yaml:"status"`
	Prompt      string            `yaml:"prompt"`
	Artifacts   []string          `yaml:"artifacts"`
	Result      string            `yaml:"result,omitempty"`
	Error       string            `yaml:"error,omitempty"`
	StartedAt   *time.Time        `yaml:"started_at,omitempty"`
	FinishedAt  *time.Time        `yaml:"finished_at,omitempty"`
	Meta        map[string]string `yaml:"meta,omitempty"`
}

// TaskFile is the top-level structure of tasks.yaml.
type TaskFile struct {
	Tasks []Task `yaml:"tasks"`
}
