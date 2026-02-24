package task

import "time"

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusReview     = "review"
	StatusMerged     = "merged"
	StatusFailed     = "failed"
)

type Task struct {
	ID          string            `yaml:"id"`
	Description string            `yaml:"description"`
	Repo        string            `yaml:"repo"`
	Branch      string            `yaml:"branch,omitempty"`
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

type TaskFile struct {
	Tasks []Task `yaml:"tasks"`
}
