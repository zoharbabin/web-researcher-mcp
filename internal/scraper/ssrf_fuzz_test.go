package scraper

import (
	"net"
	"testing"
)

// Fuzz targets for issue #499: isBlockedHostname/isPrivateIP gate every
// outbound scrape request (via NewSSRFSafeClient's DialContext) against
// attacker-influenced input — an arbitrary URL/hostname from a tool call or a
// redirect target. Both functions are pure (no I/O), so — exactly like
// FuzzSanitizeHTML/FuzzSanitizeText in internal/content/sanitize_fuzz_test.go
// — no harness/network mocking is needed. The only invariant checked is "does
// not panic": neither function has a documented contract beyond that, so a
// panic here is the actual bug class being hunted (adversarial Unicode
// through strings.ToLower/HasSuffix, or malformed byte lengths through
// net.IP/To4/Contains).

func FuzzIsBlockedHostname(f *testing.F) {
	seeds := []string{
		// Every entry in blockedHostnames (ssrf.go), verbatim.
		"metadata.google.internal",
		"metadata.azure.com",
		"metadata.tencentyun.com",
		"169.254.169.254",
		"192.0.0.192",
		"100.100.100.200",
		"instance-data",
		"kubernetes.default.svc",
		"svc.cluster.local",
		// Suffix-match case: a subdomain of a blocked suffix.
		"foo.svc.cluster.local",
		"pod.kubernetes.default.svc",
		// Bypass attempts: trailing dot, mixed case.
		"metadata.google.internal.",
		"METADATA.GOOGLE.INTERNAL",
		"Metadata.Google.Internal",
		"SVC.CLUSTER.LOCAL",
		"svc.cluster.local.",
		// Documented non-match: different registrable domain, not a suffix
		// bypass — must NOT match.
		"svc.cluster.local.evil.com",
		// Empty string.
		"",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, host string) {
		isBlockedHostname(host)
	})
}

func FuzzIsPrivateIP(f *testing.F) {
	seeds := [][]byte{
		// --- IPv4 boundaries: network / broadcast / first-outside for each
		// CIDR in isPrivateIP's privateRanges. ---
		{127, 0, 0, 0}, {127, 255, 255, 255}, {126, 255, 255, 255}, {128, 0, 0, 0}, // 127.0.0.0/8
		{10, 0, 0, 0}, {10, 255, 255, 255}, {9, 255, 255, 255}, {11, 0, 0, 0}, // 10.0.0.0/8
		{172, 16, 0, 0}, {172, 31, 255, 255}, {172, 15, 255, 255}, {172, 32, 0, 0}, // 172.16.0.0/12
		{192, 168, 0, 0}, {192, 168, 255, 255}, {192, 167, 255, 255}, {192, 169, 0, 0}, // 192.168.0.0/16
		{169, 254, 0, 0}, {169, 254, 255, 255}, {169, 253, 255, 255}, {169, 255, 0, 0}, // 169.254.0.0/16
		{100, 64, 0, 0}, {100, 127, 255, 255}, {100, 63, 255, 255}, {100, 128, 0, 0}, // 100.64.0.0/10
		{0, 0, 0, 0}, {0, 255, 255, 255}, {1, 0, 0, 0}, // 0.0.0.0/8
		{192, 0, 0, 0}, {192, 0, 0, 255}, {192, 0, 1, 0}, // 192.0.0.0/24
		{192, 0, 2, 0}, {192, 0, 2, 255}, {192, 0, 3, 0}, // 192.0.2.0/24
		{198, 51, 100, 0}, {198, 51, 100, 255}, {198, 51, 101, 0}, // 198.51.100.0/24
		{203, 0, 113, 0}, {203, 0, 113, 255}, {203, 0, 114, 0}, // 203.0.113.0/24
		{198, 18, 0, 0}, {198, 19, 255, 255}, {198, 20, 0, 0}, // 198.18.0.0/15
		{224, 0, 0, 0}, {239, 255, 255, 255}, // 224.0.0.0/4
		{240, 0, 0, 0}, {255, 255, 255, 255}, // 240.0.0.0/4 (top of range, no "outside")
		// Public IPv4 (must NOT be blocked).
		{8, 8, 8, 8}, {1, 1, 1, 1},

		// --- IPv6 boundaries for isPrivateIP's ipv6Ranges, and the documented
		// "IPv4-mapped IPv6" path (::ffff:x.x.x.x, decoded via To4()). ---
		net.ParseIP("::1").To16(),
		net.ParseIP("::").To16(),
		net.ParseIP("fc00::").To16(),
		net.ParseIP("fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").To16(), // top of fc00::/7
		net.ParseIP("fe00::").To16(),                                  // just outside fc00::/7, below fe80::/10
		net.ParseIP("fe80::").To16(),
		net.ParseIP("febf:ffff:ffff:ffff:ffff:ffff:ffff:ffff").To16(), // top of fe80::/10
		net.ParseIP("fec0::").To16(),                                  // just outside fe80::/10
		net.ParseIP("ff00::").To16(),
		net.ParseIP("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").To16(), // top of ff00::/8
		// Public/documentation IPv6 (must NOT be blocked).
		net.ParseIP("2001:db8::1").To16(),
		// IPv4-mapped IPv6 forms.
		net.ParseIP("::ffff:127.0.0.1").To16(),
		net.ParseIP("::ffff:10.0.0.1").To16(),
		net.ParseIP("::ffff:8.8.8.8").To16(),

		// --- Malformed byte lengths (neither 4 nor 16). ---
		{},
		{0},
		{1, 2, 3},
		{1, 2, 3, 4, 5},
		make([]byte, 15),
		make([]byte, 17),
		make([]byte, 32),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		// Exercise both the 4-byte and 16-byte net.IP construction paths (the
		// To4() conversion inside isPrivateIP behaves differently depending
		// on slice length), plus the raw/malformed-length slice as-is.
		isPrivateIP(net.IP(b))
		if len(b) >= 4 {
			isPrivateIP(net.IP(b[:4]))
		}
		if len(b) >= 16 {
			isPrivateIP(net.IP(b[:16]))
		}
	})
}
