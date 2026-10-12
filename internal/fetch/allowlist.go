package fetch

import "strings"

// allowlist is the set of hosts images may be fetched from.
type allowlist struct {
	all      bool
	exact    map[string]bool
	suffixes []string
}

// newAllowlist returns a new allowlist from a list of entries, each one of:
//   - a host name or IP address, matched exactly, ignoring case
//   - "*.example.com", matching every host name under example.com, but not
//     example.com itself
//   - "*", matching every host
//
// Any other entry matches nothing.
func newAllowlist(entries []string) allowlist {
	a := allowlist{exact: map[string]bool{}}

	for _, e := range entries {
		e = strings.ToLower(e)

		switch {
		case e == "*":
			a.all = true
		case strings.HasPrefix(e, "*.") && len(e) > len("*."):
			a.suffixes = append(a.suffixes, e[1:])
		default:
			a.exact[e] = true
		}
	}

	return a
}

// allows reports whether host may be fetched from.
func (a allowlist) allows(host string) bool {
	host = strings.ToLower(host)
	if a.all || a.exact[host] {
		return true
	}

	for _, s := range a.suffixes {
		if strings.HasSuffix(host, s) && len(host) > len(s) {
			return true
		}
	}

	return false
}
