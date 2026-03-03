package cli

import (
	"testing"
)

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]string
		want string
	}{
		{"nil meta", nil, ""},
		{"empty meta", map[string]string{}, ""},
		{"tokens only", map[string]string{
			"input_tokens": "150000", "output_tokens": "50000",
		}, "150k/50k"},
		{"with cost", map[string]string{
			"input_tokens": "150000", "output_tokens": "50000", "cost_usd": "1.23",
		}, "150k/50k ($1.23)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTokens(tt.meta)
			if got != tt.want {
				t.Errorf("formatTokens() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseFloat(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"", 0},
		{"1.23", 1.23},
		{"0.0045", 0.0045},
		{"invalid", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseFloat(tt.input)
			if got != tt.want {
				t.Errorf("parseFloat(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestAtoi(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"12345", 12345},
		{"1,234", 1234},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := atoi(tt.input)
			if got != tt.want {
				t.Errorf("atoi(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
