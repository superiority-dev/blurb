package httpapi

import (
	"encoding/json"
	"testing"
)

// TestAspect requires aspect to round to four decimals, halves up, and to
// marshal the same on every architecture.
func TestAspect(t *testing.T) {
	cases := []struct {
		width, height int
		want          string
	}{
		{width: 1920, height: 1080, want: "1.7778"},
		{width: 1080, height: 1920, want: "0.5625"},
		{width: 1, height: 1, want: "1"},
		{width: 3, height: 2, want: "1.5"},
		{width: 2, height: 3, want: "0.6667"},
		{width: 1, height: 3, want: "0.3333"},
		{width: 1, height: 32, want: "0.0313"},
		{width: 3, height: 32, want: "0.0938"},
		{width: 1, height: 20_000, want: "0.0001"},
		{width: 1, height: 20_001, want: "0"},
		{width: 20_000, height: 1, want: "20000"},
		{width: 1_000_000_000, height: 1, want: "1000000000"},
	}

	for _, tc := range cases {
		b, err := json.Marshal(aspect(tc.width, tc.height))
		if err != nil || string(b) != tc.want {
			t.Errorf("aspect(%d, %d) = %s, %v; want %s", tc.width, tc.height, b, err, tc.want)
		}
	}
}

func TestHex64(t *testing.T) {
	if got := hex64(1); got != "0000000000000001" {
		t.Errorf("hex64(1) = %q, want 16 digits", got)
	}
}
