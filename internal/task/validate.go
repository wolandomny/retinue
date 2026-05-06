package task

import (
	"fmt"

	"github.com/wolandomny/retinue/internal/effort"
)

// validateFields checks per-task field-level constraints (currently
// just the optional Effort enum). DAG-level checks (cycles, unknown
// deps) live in Validate over in dag.go.
func validateFields(tasks []Task) error {
	for i, t := range tasks {
		if err := effort.Validate(t.Effort); err != nil {
			return fmt.Errorf("tasks.yaml: task[%d] (%s): %w", i, t.ID, err)
		}
	}
	return nil
}
