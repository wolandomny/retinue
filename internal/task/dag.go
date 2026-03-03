package task

import "fmt"

// Ready returns tasks that are pending and have all dependencies satisfied
// (i.e., all dependencies are in done or merged status).
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
		case StatusDone, StatusMerged:
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

// ArtifactOverlap describes two independent tasks that share an artifact.
type ArtifactOverlap struct {
	File  string
	TaskA string
	TaskB string
}

// OverlapWarnings checks for artifact overlaps between tasks that
// have no dependency relationship (neither directly nor transitively).
// Returns a list of overlaps. An empty list means no concerns.
func OverlapWarnings(tasks []Task) []ArtifactOverlap {
	// Build a set of all transitive dependencies for each task.
	depGraph := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		depGraph[t.ID] = t.DependsOn
	}

	// Compute transitive closure: for each task, the full set of
	// ancestors and descendants.
	related := make(map[string]map[string]bool) // taskID -> set of related taskIDs
	for _, t := range tasks {
		if related[t.ID] == nil {
			related[t.ID] = make(map[string]bool)
		}
		// Walk up: all transitive dependencies.
		var walkUp func(id string)
		walkUp = func(id string) {
			for _, dep := range depGraph[id] {
				if !related[t.ID][dep] {
					related[t.ID][dep] = true
					if related[dep] == nil {
						related[dep] = make(map[string]bool)
					}
					related[dep][t.ID] = true
					walkUp(dep)
				}
			}
		}
		walkUp(t.ID)
	}

	// Build artifact -> list of task IDs.
	artifactOwners := make(map[string][]string)
	for _, t := range tasks {
		// Skip completed tasks — they've already merged.
		if t.Status == StatusMerged || t.Status == StatusDone {
			continue
		}
		for _, art := range t.Artifacts {
			artifactOwners[art] = append(artifactOwners[art], t.ID)
		}
	}

	// Find overlaps between unrelated tasks.
	var overlaps []ArtifactOverlap
	seen := make(map[string]bool) // "taskA:taskB:file" dedup key

	for file, owners := range artifactOwners {
		for i := 0; i < len(owners); i++ {
			for j := i + 1; j < len(owners); j++ {
				a, b := owners[i], owners[j]
				// Check if a and b are related (have a dependency path).
				if related[a] != nil && related[a][b] {
					continue // related tasks — overlap is expected
				}

				key := a + ":" + b + ":" + file
				if seen[key] {
					continue
				}
				seen[key] = true

				overlaps = append(overlaps, ArtifactOverlap{
					File:  file,
					TaskA: a,
					TaskB: b,
				})
			}
		}
	}

	return overlaps
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
