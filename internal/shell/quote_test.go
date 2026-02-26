package shell

import "testing"

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"hello", "'hello'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{"a b c", "'a b c'"},
	}
	for _, tt := range tests {
		got := Quote(tt.in)
		if got != tt.want {
			t.Errorf("Quote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestJoin(t *testing.T) {
	got := Join([]string{"echo", "hello world", "it's"})
	want := "'echo' 'hello world' 'it'\\''s'"
	if got != want {
		t.Errorf("Join() = %q, want %q", got, want)
	}
}
