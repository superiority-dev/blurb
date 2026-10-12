package ssrf

import (
	"errors"
	"net/netip"
	"testing"
)

func TestControl(t *testing.T) {
	cases := map[string]error{
		"127.0.0.1:80": ErrNotAllowed,
		"[::1]:443":    ErrNotAllowed,
		"8.8.8.8:80":   nil,
		"localhost:80": ErrNotAllowed,
	}

	for address, want := range cases {
		if err := Control("tcp", address, nil); !errors.Is(err, want) {
			t.Errorf("Control(%q) = %v, want %v", address, err, want)
		}
	}
}

func TestAllowed(t *testing.T) {
	for addr, want := range addrs {
		if got := allowed(netip.MustParseAddr(addr)); got != want {
			t.Errorf("allowed(%s) = %t, want %t", addr, got, want)
		}
	}
}

// addrs are addresses and whether [Control] lets them through.
var addrs = map[string]bool{
	"0.0.0.0":          false,
	"10.1.2.3":         false,
	"100.64.0.1":       false,
	"127.0.0.1":        false,
	"169.254.169.254":  false,
	"172.16.0.1":       false,
	"192.0.0.1":        false,
	"192.0.2.1":        false,
	"192.88.99.2":      false,
	"192.168.1.1":      false,
	"198.18.0.1":       false,
	"198.51.100.1":     false,
	"203.0.113.1":      false,
	"224.0.0.1":        false,
	"255.255.255.255":  false,
	"::1":              false,
	"64:ff9b:1::1":     false,
	"100::1":           false,
	"100:0:0:1::1":     false,
	"2001::1":          false,
	"2001:db8::1":      false,
	"2002:808:808::1":  false,
	"3fff::1":          false,
	"5f00::1":          false,
	"fd00::1":          false,
	"fe80::1":          false,
	"ff02::1":          false,
	"::ffff:127.0.0.1": false,
	"64:ff9b::a00:1":   false,
	"fe80::1%eth0":     false,
	"8.8.8.8":          true,
	"2606:4700::1111":  true,
	"::ffff:8.8.8.8":   true,
	"64:ff9b::808:808": true,
}
