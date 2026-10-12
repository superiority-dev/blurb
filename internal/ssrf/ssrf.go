// Package ssrf protects against server-side request forgery (SSRF) by
// refusing connections to addresses such as loopback, private networks and
// link-local addresses.
package ssrf

import (
	"errors"
	"net/netip"
	"syscall"
)

// ErrNotAllowed is returned by [Control] for an address it refuses.
var ErrNotAllowed = errors.New("ssrf: address not allowed")

// nat64 is the well-known NAT64 prefix; its addresses carry an IPv4 address
// in their last 32 bits.
var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// Control is a [net.Dialer] Control function. Called with the resolved
// address about to be connected, it returns [ErrNotAllowed] for an address:
//   - in a block IANA's special-purpose address registries mark not globally
//     reachable, such as loopback, private, link-local or documentation
//     addresses
//   - that is multicast, IPv4-compatible (::/96, which holds :: and ::1) or
//     6to4 (2002::/16)
//
// An IPv4-mapped (::ffff:0:0/96) or NAT64 (64:ff9b::/96) address is judged
// instead by the IPv4 address inside it.
func Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !allowed(ap.Addr()) {
		return ErrNotAllowed
	}

	return nil
}

// allowed reports whether [Control] lets a connection to addr through.
func allowed(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")

	if nat64.Contains(addr) {
		b := addr.As16()
		addr = netip.AddrFrom4([4]byte(b[len(b)-4:]))
	}

	for _, p := range refusedPrefixes {
		if p.Contains(addr) {
			return false
		}
	}

	return true
}
