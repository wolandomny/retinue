package standing

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAgentYAMLRoundTrip(t *testing.T) {
	agent := Agent{
		ID:       "azazello",
		Name:     "Azazello",
		Role:     "CI Watcher",
		Repos:    []string{"api", "frontend"},
		Schedule: "every 4h",
		Model:    "claude-sonnet-4-20250514",
		Prompt:   "Watch CI pipelines and report failures.",
		Enabled:  true,
	}

	data, err := yaml.Marshal(&agent)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got Agent
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.ID != agent.ID {
		t.Errorf("ID = %q, want %q", got.ID, agent.ID)
	}
	if got.Name != agent.Name {
		t.Errorf("Name = %q, want %q", got.Name, agent.Name)
	}
	if got.Role != agent.Role {
		t.Errorf("Role = %q, want %q", got.Role, agent.Role)
	}
	if len(got.Repos) != len(agent.Repos) {
		t.Errorf("Repos length = %d, want %d", len(got.Repos), len(agent.Repos))
	}
	if got.Schedule != agent.Schedule {
		t.Errorf("Schedule = %q, want %q", got.Schedule, agent.Schedule)
	}
	if got.Model != agent.Model {
		t.Errorf("Model = %q, want %q", got.Model, agent.Model)
	}
	if got.Prompt != agent.Prompt {
		t.Errorf("Prompt = %q, want %q", got.Prompt, agent.Prompt)
	}
	if got.Enabled != agent.Enabled {
		t.Errorf("Enabled = %v, want %v", got.Enabled, agent.Enabled)
	}
}

func TestAgentOmitemptyBehavior(t *testing.T) {
	// An agent with only required fields — optional fields should be omitted.
	agent := Agent{
		ID:     "behemoth",
		Name:   "Behemoth",
		Prompt: "Guard the codebase.",
	}

	data, err := yaml.Marshal(&agent)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	s := string(data)

	// Required fields must be present.
	if !strings.Contains(s, "id:") {
		t.Error("YAML should contain 'id' field")
	}
	if !strings.Contains(s, "name:") {
		t.Error("YAML should contain 'name' field")
	}
	if !strings.Contains(s, "prompt:") {
		t.Error("YAML should contain 'prompt' field")
	}

	// Optional fields with zero values should be omitted.
	if strings.Contains(s, "role:") {
		t.Error("YAML should not contain 'role' when empty")
	}
	if strings.Contains(s, "repos:") {
		t.Error("YAML should not contain 'repos' when nil")
	}
	if strings.Contains(s, "schedule:") {
		t.Error("YAML should not contain 'schedule' when empty")
	}
	if strings.Contains(s, "model:") {
		t.Error("YAML should not contain 'model' when empty")
	}
	if strings.Contains(s, "enabled:") {
		t.Error("YAML should not contain 'enabled' when false (zero value)")
	}
}

func TestAgentEnabledDefault(t *testing.T) {
	// Verify that the zero value for Enabled is false — agents are
	// explicitly opted in.
	var agent Agent
	if agent.Enabled {
		t.Error("default Enabled should be false (Go zero value)")
	}
}

func TestAgentFileYAMLRoundTrip(t *testing.T) {
	af := AgentFile{
		Agents: []Agent{
			{
				ID:     "azazello",
				Name:   "Azazello",
				Prompt: "Watch CI.",
			},
			{
				ID:      "behemoth",
				Name:    "Behemoth",
				Prompt:  "Guard the codebase.",
				Enabled: true,
			},
		},
	}

	data, err := yaml.Marshal(&af)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got AgentFile
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(got.Agents) != len(af.Agents) {
		t.Fatalf("Agents length = %d, want %d", len(got.Agents), len(af.Agents))
	}

	for i, want := range af.Agents {
		if got.Agents[i].ID != want.ID {
			t.Errorf("agent[%d].ID = %q, want %q", i, got.Agents[i].ID, want.ID)
		}
		if got.Agents[i].Prompt != want.Prompt {
			t.Errorf("agent[%d].Prompt = %q, want %q", i, got.Agents[i].Prompt, want.Prompt)
		}
		if got.Agents[i].Enabled != want.Enabled {
			t.Errorf("agent[%d].Enabled = %v, want %v", i, got.Agents[i].Enabled, want.Enabled)
		}
	}
}
