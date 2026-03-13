package phone

import (
	"testing"
)

func TestIsKillWord(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"back", true},
		{"Back", true},
		{"BACK", true},
		{"/desk", true},
		{"/Desk", true},
		{"at my desk", true},
		{"At My Desk", true},
		{"i'm back", true},
		{"I'm Back", true},
		{"im back", true},
		{"Im Back", true},
		{"  back  ", true},  // whitespace trimmed
		{"back!", false},     // not exact match
		{"I'm back!", false}, // not exact match
		{"going back", false},
		{"hello", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isKillWord(tt.input)
			if got != tt.want {
				t.Errorf("isKillWord(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestEscapeTmux(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "semicolons",
			input: "foo; bar; baz",
			want:  `foo\; bar\; baz`,
		},
		{
			name:  "dollar signs",
			input: "echo $HOME $USER",
			want:  `echo \$HOME \$USER`,
		},
		{
			name:  "backticks",
			input: "run `command`",
			want:  "run \\`command\\`",
		},
		{
			name:  "backslashes",
			input: `path\to\file`,
			want:  `path\\to\\file`,
		},
		{
			name:  "newlines collapsed",
			input: "line1\nline2\nline3",
			want:  "line1 line2 line3",
		},
		{
			name:  "carriage returns removed",
			input: "line1\r\nline2",
			want:  "line1 line2",
		},
		{
			name:  "combined special chars",
			input: "echo $HOME; ls `pwd`",
			want:  `echo \$HOME\; ls \` + "`" + `pwd\` + "`",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EscapeTmux(tt.input)
			if got != tt.want {
				t.Errorf("EscapeTmux(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
