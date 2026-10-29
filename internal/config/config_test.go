package config

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"0812-3456-7890":   "6281234567890",
		"+62 812 3456 789": "628123456789",
		"6281234567890":    "6281234567890",
		"":                 "",
	}
	for in, want := range cases {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q; want %q", in, got, want)
		}
	}
}
