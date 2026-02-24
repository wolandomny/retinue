package task

import "fmt"

// Ready returns tasks that are pending and have all dependencies satisfied
// (i.e., all dependencies are in done, review, or merged status).
func Ready(tasks []Task) []Task {
	statusByID := make(map[string]string, len(tasks))
	for _, t := range tasks {
		statusByID[t.ID] = t.Status
	}

	var ready []Task
	for _, t := range tasks {
		if t.Status != StatusPending {
			continue
		}
		if depsResolved(t.DependsOn, statusByID) {
			ready = append(ready, t)
		}
	}
	return ready
}

func depsResolved(deps []string, statusByID map[string]string) bool {
	for _, dep := range deps {
		status, ok := statusByID[dep]
		if !ok {
			return false
		}
		switch status {
		case StatusDone, StatusReview, StatusMerged:
			// resolved
		default:
			return false
		}
	}
	return true
}

// Validate checks for missing dependencies and cycles in the task DAG.
func Validate(tasks []Task) error {
	ids := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		ids[t.ID] = true
	}

	// Check for missing dependencies.
	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("task %q depends on unknown task %q", t.ID, dep)
			}
		}
	}

	// Check for cycles using DFS.
	const (
		white = 0 // unvisited
		gray  = 1 // in current path
		black = 2 // fully explored
	)

	color := make(map[string]int, len(tasks))
	depsMap := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		depsMap[t.ID] = t.DependsOn
	}

	var visit func(id string) error
	visit = func(id string) error {
		color[id] = gray
		for _, dep := range depsMap[id] {
			switch color[dep] {
			case gray:
				return fmt.Errorf("cycle detected involving task %q", dep)
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}

	for _, t := range tasks {
		if color[t.ID] == white {
			if err := visit(t.ID); err != nil {
				return err
			}
		}
	}

	return nil
}

// TopologicalOrder returns tasks sorted so that dependencies come before dependents.
func TopologicalOrder(tasks []Task) ([]Task, error) {
	if err := Validate(tasks); err != nil {
		return nil, err
	}

	byID := make(map[string]Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}

	visited := make(map[string]bool, len(tasks))
	var order []Task

	var visit func(id string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		t := byID[id]
		for _, dep := range t.DependsOn {
			visit(dep)
		}
		order = append(order, t)
	}

	for _, t := range tasks {
		visit(t.ID)
	}

	return order, nil
}
