package ssrf

import "net/netip"

// refusedPrefixes are the blocks [Control] refuses: every block IANA's
// special-purpose address registries mark not globally reachable, plus
// multicast, ::/96 and 6to4, leaving out any block inside another listed one.
// IPv4-mapped and NAT64 addresses are judged by the IPv4 address inside them,
// so neither block is listed.
var refusedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "This network"
	netip.MustParsePrefix("10.0.0.0/8"),      // Private-Use
	netip.MustParsePrefix("100.64.0.0/10"),   // Shared Address Space
	netip.MustParsePrefix("127.0.0.0/8"),     // Loopback
	netip.MustParsePrefix("169.254.0.0/16"),  // Link Local
	netip.MustParsePrefix("172.16.0.0/12"),   // Private-Use
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF Protocol Assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // Documentation (TEST-NET-1)
	netip.MustParsePrefix("192.88.99.2/32"),  // 6a44-relay anycast address
	netip.MustParsePrefix("192.168.0.0/16"),  // Private-Use
	netip.MustParsePrefix("198.18.0.0/15"),   // Benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // Documentation (TEST-NET-2)
	netip.MustParsePrefix("203.0.113.0/24"),  // Documentation (TEST-NET-3)
	netip.MustParsePrefix("224.0.0.0/4"),     // IPv4 multicast
	netip.MustParsePrefix("240.0.0.0/4"),     // Reserved
	netip.MustParsePrefix("::/96"),           // IPv4-compatible, :: and ::1
	netip.MustParsePrefix("64:ff9b:1::/48"),  // Local-Use IPv4/IPv6 Translation
	netip.MustParsePrefix("100::/64"),        // Discard-Only Address Block
	netip.MustParsePrefix("100:0:0:1::/64"),  // Dummy IPv6 Prefix
	netip.MustParsePrefix("2001::/23"),       // IETF Protocol Assignments
	netip.MustParsePrefix("2001:db8::/32"),   // Documentation
	netip.MustParsePrefix("2002::/16"),       // 6to4
	netip.MustParsePrefix("3fff::/20"),       // Documentation
	netip.MustParsePrefix("5f00::/16"),       // Segment Routing (SRv6) SIDs
	netip.MustParsePrefix("fc00::/7"),        // Unique-Local
	netip.MustParsePrefix("fe80::/10"),       // Link-Local Unicast
	netip.MustParsePrefix("ff00::/8"),        // IPv6 multicast
}
