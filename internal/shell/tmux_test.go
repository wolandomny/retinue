package shell

import "testing"

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
			name:  "backslashes escaped first",
			input: `path\to\file`,
			want:  `path\\to\\file`,
		},
		{
			name:  "semicolons escaped",
			input: "foo; bar; baz",
			want:  `foo\; bar\; baz`,
		},
		{
			name:  "dollar signs escaped",
			input: "echo $HOME $USER",
			want:  `echo \$HOME \$USER`,
		},
		{
			name:  "backticks escaped",
			input: "run `command`",
			want:  "run \\`command\\`",
		},
		{
			name:  "newlines collapsed to spaces",
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
		{
			name:  "backslash before semicolon",
			input: `\;`,
			want:  `\\\;`,
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
