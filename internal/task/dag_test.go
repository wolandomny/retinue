package task

import (
	"testing"
)

func TestReady(t *testing.T) {
	tests := []struct {
		name    string
		tasks   []Task
		wantIDs []string
	}{
		{
			name:    "empty list",
			tasks:   nil,
			wantIDs: nil,
		},
		{
			name: "single pending task with no deps",
			tasks: []Task{
				{ID: "a", Status: StatusPending},
			},
			wantIDs: []string{"a"},
		},
		{
			name: "pending task with unmet dependency",
			tasks: []Task{
				{ID: "a", Status: StatusPending},
				{ID: "b", Status: StatusPending, DependsOn: []string{"a"}},
			},
			wantIDs: []string{"a"},
		},
		{
			name: "pending task with met dependency",
			tasks: []Task{
				{ID: "a", Status: StatusDone},
				{ID: "b", Status: StatusPending, DependsOn: []string{"a"}},
			},
			wantIDs: []string{"b"},
		},
		{
			name: "dependency in merged counts as resolved",
			tasks: []Task{
				{ID: "a", Status: StatusMerged},
				{ID: "b", Status: StatusPending, DependsOn: []string{"a"}},
			},
			wantIDs: []string{"b"},
		},
		{
			name: "in_progress tasks are not ready",
			tasks: []Task{
				{ID: "a", Status: StatusInProgress},
			},
			wantIDs: nil,
		},
		{
			name: "multiple ready tasks",
			tasks: []Task{
				{ID: "a", Status: StatusPending},
				{ID: "b", Status: StatusPending},
				{ID: "c", Status: StatusPending, DependsOn: []string{"a"}},
			},
			wantIDs: []string{"a", "b"},
		},
		{
			name: "dependency on unknown task blocks",
			tasks: []Task{
				{ID: "a", Status: StatusPending, DependsOn: []string{"nonexistent"}},
			},
			wantIDs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Ready(tt.tasks)
			gotIDs := make([]string, len(got))
			for i, g := range got {
				gotIDs[i] = g.ID
			}

			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("Ready() returned %v, want %v", gotIDs, tt.wantIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != tt.wantIDs[i] {
					t.Errorf("Ready()[%d] = %q, want %q", i, gotIDs[i], tt.wantIDs[i])
				}
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		tasks   []Task
		wantErr bool
	}{
		{
			name:    "empty is valid",
			tasks:   nil,
			wantErr: false,
		},
		{
			name: "simple chain is valid",
			tasks: []Task{
				{ID: "a"},
				{ID: "b", DependsOn: []string{"a"}},
				{ID: "c", DependsOn: []string{"b"}},
			},
			wantErr: false,
		},
		{
			name: "missing dependency",
			tasks: []Task{
				{ID: "a", DependsOn: []string{"nonexistent"}},
			},
			wantErr: true,
		},
		{
			name: "self-cycle",
			tasks: []Task{
				{ID: "a", DependsOn: []string{"a"}},
			},
			wantErr: true,
		},
		{
			name: "two-node cycle",
			tasks: []Task{
				{ID: "a", DependsOn: []string{"b"}},
				{ID: "b", DependsOn: []string{"a"}},
			},
			wantErr: true,
		},
		{
			name: "diamond is valid",
			tasks: []Task{
				{ID: "a"},
				{ID: "b", DependsOn: []string{"a"}},
				{ID: "c", DependsOn: []string{"a"}},
				{ID: "d", DependsOn: []string{"b", "c"}},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.tasks)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestOverlapWarnings_NoOverlap(t *testing.T) {
	tasks := []Task{
		{ID: "a", Status: StatusPending, Artifacts: []string{"file1.go"}},
		{ID: "b", Status: StatusPending, Artifacts: []string{"file2.go"}},
	}
	overlaps := OverlapWarnings(tasks)
	if len(overlaps) != 0 {
		t.Errorf("expected no overlaps, got %d", len(overlaps))
	}
}

func TestOverlapWarnings_IndependentOverlap(t *testing.T) {
	tasks := []Task{
		{ID: "a", Status: StatusPending, Artifacts: []string{"shared.go"}},
		{ID: "b", Status: StatusPending, Artifacts: []string{"shared.go"}},
	}
	overlaps := OverlapWarnings(tasks)
	if len(overlaps) != 1 {
		t.Fatalf("expected 1 overlap, got %d", len(overlaps))
	}
	if overlaps[0].File != "shared.go" {
		t.Errorf("expected file 'shared.go', got %q", overlaps[0].File)
	}
}

func TestOverlapWarnings_DependentOverlapOK(t *testing.T) {
	// Tasks with a dependency edge can share artifacts — that's expected.
	tasks := []Task{
		{ID: "a", Status: StatusPending, Artifacts: []string{"shared.go"}},
		{ID: "b", Status: StatusPending, Artifacts: []string{"shared.go"}, DependsOn: []string{"a"}},
	}
	overlaps := OverlapWarnings(tasks)
	if len(overlaps) != 0 {
		t.Errorf("expected no overlaps for dependent tasks, got %d", len(overlaps))
	}
}

func TestOverlapWarnings_TransitiveDependencyOK(t *testing.T) {
	// a -> b -> c: a and c are transitively related, overlap is fine.
	tasks := []Task{
		{ID: "a", Status: StatusPending, Artifacts: []string{"shared.go"}},
		{ID: "b", Status: StatusPending, DependsOn: []string{"a"}},
		{ID: "c", Status: StatusPending, Artifacts: []string{"shared.go"}, DependsOn: []string{"b"}},
	}
	overlaps := OverlapWarnings(tasks)
	if len(overlaps) != 0 {
		t.Errorf("expected no overlaps for transitively related tasks, got %d", len(overlaps))
	}
}

func TestOverlapWarnings_SkipsMergedTasks(t *testing.T) {
	tasks := []Task{
		{ID: "a", Status: StatusMerged, Artifacts: []string{"shared.go"}},
		{ID: "b", Status: StatusPending, Artifacts: []string{"shared.go"}},
	}
	overlaps := OverlapWarnings(tasks)
	if len(overlaps) != 0 {
		t.Errorf("expected no overlaps when one task is merged, got %d", len(overlaps))
	}
}

func TestOverlapWarnings_MultipleFiles(t *testing.T) {
	tasks := []Task{
		{ID: "a", Status: StatusPending, Artifacts: []string{"x.go", "y.go"}},
		{ID: "b", Status: StatusPending, Artifacts: []string{"y.go", "z.go"}},
	}
	overlaps := OverlapWarnings(tasks)
	if len(overlaps) != 1 {
		t.Fatalf("expected 1 overlap (y.go only), got %d", len(overlaps))
	}
	if overlaps[0].File != "y.go" {
		t.Errorf("expected file 'y.go', got %q", overlaps[0].File)
	}
}

func TestTopologicalOrder(t *testing.T) {
	tasks := []Task{
		{ID: "c", DependsOn: []string{"a", "b"}},
		{ID: "a"},
		{ID: "b", DependsOn: []string{"a"}},
	}

	order, err := TopologicalOrder(tasks)
	if err != nil {
		t.Fatalf("TopologicalOrder() error = %v", err)
	}

	pos := make(map[string]int, len(order))
	for i, t := range order {
		pos[t.ID] = i
	}

	// a must come before b and c
	if pos["a"] >= pos["b"] {
		t.Errorf("a (pos %d) should come before b (pos %d)", pos["a"], pos["b"])
	}
	if pos["a"] >= pos["c"] {
		t.Errorf("a (pos %d) should come before c (pos %d)", pos["a"], pos["c"])
	}
	// b must come before c
	if pos["b"] >= pos["c"] {
		t.Errorf("b (pos %d) should come before c (pos %d)", pos["b"], pos["c"])
	}
}

func TestTopologicalOrderCycleError(t *testing.T) {
	tasks := []Task{
		{ID: "a", DependsOn: []string{"b"}},
		{ID: "b", DependsOn: []string{"a"}},
	}

	_, err := TopologicalOrder(tasks)
	if err == nil {
		t.Fatal("TopologicalOrder() expected error for cycle, got nil")
	}
}
