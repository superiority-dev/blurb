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
	list := allowlist{exact: map[string]bool{}}

	for _, entry := range entries {
		entry = strings.ToLower(entry)

		switch {
		case entry == "*":
			list.all = true
		case strings.HasPrefix(entry, "*.") && len(entry) > len("*."):
			list.suffixes = append(list.suffixes, entry[1:])
		default:
			list.exact[entry] = true
		}
	}

	return list
}

// allows reports whether host may be fetched from.
func (a allowlist) allows(host string) bool {
	host = strings.ToLower(host)
	if a.all || a.exact[host] {
		return true
	}

	for _, suffix := range a.suffixes {
		if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
			return true
		}
	}

	return false
}
