package fetch

import "testing"

func TestNewAllowlistEntries(t *testing.T) {
	a := newAllowlist(allowlistEntries)

	for host, want := range allowlistHosts {
		if got := a.allows(host); got != want {
			t.Errorf("allows(%q) = %t, want %t", host, got, want)
		}
	}
}

func TestNewAllowlistAll(t *testing.T) {
	a := newAllowlist([]string{"*"})
	if !a.allows("anything.example") {
		t.Error("* does not allow every host")
	}
}

var allowlistEntries = []string{"Images.Example.com", "*.cdn.example", "10.0.0.5", "fd00::1", "*."}

// allowlistHosts are hosts and whether allowlistEntries allow them.
var allowlistHosts = map[string]bool{
	"images.example.com":      true,
	"IMAGES.EXAMPLE.COM":      true,
	"example.com":             false,
	"a.images.example.com":    false,
	"evil-images.example.com": false,
	"a.b.cdn.example":         true,
	"cdn.example":             false,
	".cdn.example":            false,
	"xcdn.example":            false,
	"10.0.0.5":                true,
	"fd00::1":                 true,
	"evil.com.":               false,
}
