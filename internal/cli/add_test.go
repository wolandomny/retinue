package cli

import "testing"

func TestRepoNameFromURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github.com/org/api", "api"},
		{"github.com/org/web", "web"},
		{"gitlab.com/team/some-service", "some-service"},
		{"github.com/org/repo.git", "repo.git"},
		{"single-segment", "single-segment"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := repoNameFromURL(tt.input)
			if got != tt.want {
				t.Errorf("repoNameFromURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
