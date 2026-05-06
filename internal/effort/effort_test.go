package effort

import (
	"strings"
	"testing"
)

func TestValidate_Empty(t *testing.T) {
	if err := Validate(""); err != nil {
		t.Errorf("Validate(\"\") = %v, want nil (empty string means unset)", err)
	}
}

func TestValidate_AllValidLevels(t *testing.T) {
	for _, level := range Levels {
		t.Run(level, func(t *testing.T) {
			if err := Validate(level); err != nil {
				t.Errorf("Validate(%q) = %v, want nil", level, err)
			}
		})
	}
}

func TestValidate_Invalid(t *testing.T) {
	cases := []string{"ultra", "LOW", "Low", "extreme", "x-high", "off", " low"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate(%q) = nil, want error", c)
			}
			if !strings.Contains(err.Error(), "invalid effort") {
				t.Errorf("error should mention 'invalid effort', got: %v", err)
			}
			if !strings.Contains(err.Error(), c) {
				t.Errorf("error should mention the bad value %q, got: %v", c, err)
			}
		})
	}
}
