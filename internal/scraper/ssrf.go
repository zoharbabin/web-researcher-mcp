package scraper

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrSSRFBlocked = errors.New("ssrf: request blocked (private IP or blocked hostname)")

// blockedHostnames lists cloud-provider link-local metadata endpoints and
// in-cluster service hostnames that must never be reachable via user-supplied
// URLs. Entries are matched case-insensitively as an exact hostname or as a
// dot-bounded suffix (see isBlockedHostname), so "kubernetes.default.svc"
// matches itself and "*.svc.cluster.local" matches any in-cluster service.
var blockedHostnames = []string{
	"metadata.google.internal",
	"metadata.azure.com",
	"metadata.tencentyun.com",
	"169.254.169.254", // AWS / Azure / GCP / DigitalOcean / OpenStack link-local
	"192.0.0.192",     // Oracle Cloud metadata
	"100.100.100.200", // Alibaba Cloud metadata
	"instance-data",
	"kubernetes.default.svc",
	"svc.cluster.local",
}

// maxRedirects bounds every scraper HTTP client's auto-followed redirect
// chain — shared by the plain SSRF-safe client here and the stealth tier's
// browser-header client (stealth.go), so the two never drift to different
// caps. 10 (rather than the previous 5) accommodates multi-hop cookie/IDP
// consent-handshake redirect chains some publishers use (e.g. nature.com's
// "grover" cookie-authorization dance can take 6-7 hops before landing on the
// real page, #631) while still bounding an actual redirect loop.
const maxRedirects = 10

func NewSSRFSafeClient(allowPrivate bool) *http.Client {
	return NewSSRFSafeClientWithTimeout(allowPrivate, 30*time.Second)
}

// NewSSRFSafeClientWithTimeout is NewSSRFSafeClient with a caller-chosen
// timeout, for upstreams whose observed latency (e.g. Wayback CDX under load)
// regularly exceeds the 30s default.
func NewSSRFSafeClientWithTimeout(allowPrivate bool, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: newSSRFSafeTransport(allowPrivate),
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

func newSSRFSafeTransport(allowPrivate bool) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			if isBlockedHostname(host) {
				return nil, ErrSSRFBlocked
			}

			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}

			if len(ips) == 0 {
				return nil, fmt.Errorf("no IP addresses found for %s", host)
			}

			for _, ip := range ips {
				if !allowPrivate && isPrivateIP(ip.IP) {
					return nil, ErrSSRFBlocked
				}
			}

			// Connect to the first resolved IP directly (prevents DNS rebinding)
			target := net.JoinHostPort(ips[0].IP.String(), port)
			return (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext(ctx, network, target)
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// isBlockedHostname reports whether host matches a blocked metadata/in-cluster
// endpoint. Matching is case-insensitive and either exact or dot-bounded suffix:
// "svc.cluster.local" matches "foo.svc.cluster.local" but NOT
// "svc.cluster.local.evil.com" (the latter is a different registrable domain).
func isBlockedHostname(host string) bool {
	hostLower := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, blocked := range blockedHostnames {
		if hostLower == blocked || strings.HasSuffix(hostLower, "."+blocked) {
			return true
		}
	}
	return false
}

func isPrivateIP(ip net.IP) bool {
	privateRanges := []struct {
		network *net.IPNet
	}{
		{mustParseCIDR("127.0.0.0/8")},
		{mustParseCIDR("10.0.0.0/8")},
		{mustParseCIDR("172.16.0.0/12")},
		{mustParseCIDR("192.168.0.0/16")},
		{mustParseCIDR("169.254.0.0/16")},
		{mustParseCIDR("100.64.0.0/10")},
		{mustParseCIDR("0.0.0.0/8")},
		{mustParseCIDR("192.0.0.0/24")},
		{mustParseCIDR("192.0.2.0/24")},
		{mustParseCIDR("198.51.100.0/24")},
		{mustParseCIDR("203.0.113.0/24")},
		{mustParseCIDR("198.18.0.0/15")},
		{mustParseCIDR("224.0.0.0/4")},
		{mustParseCIDR("240.0.0.0/4")},
	}

	ip4 := ip.To4()
	if ip4 != nil {
		for _, r := range privateRanges {
			if r.network.Contains(ip4) {
				return true
			}
		}
		return false
	}

	// IPv6 checks
	ipv6Ranges := []struct {
		network *net.IPNet
	}{
		{mustParseCIDR("::1/128")},
		{mustParseCIDR("fc00::/7")},
		{mustParseCIDR("fe80::/10")},
		{mustParseCIDR("ff00::/8")},
		{mustParseCIDR("::/128")},
	}

	for _, r := range ipv6Ranges {
		if r.network.Contains(ip) {
			return true
		}
	}

	// Check IPv4-mapped IPv6
	if ip4mapped := ip.To4(); ip4mapped != nil {
		for _, r := range privateRanges {
			if r.network.Contains(ip4mapped) {
				return true
			}
		}
	}

	return false
}

// mustParseCIDR parses a constant CIDR string at package-init time and panics
// on error. Contract: callers MUST pass only compile-time-constant CIDR
// literals (never user input), so a panic here means a programmer typo, not a
// runtime/request-path failure. Validated by TestPrivateRangesParse.
func mustParseCIDR(s string) *net.IPNet {
	_, network, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return network
}

// ValidateHost applies the dial-time SSRF rules ahead of time: it fails when
// host is a blocked metadata name, resolves to nothing, or (unless allowPrivate)
// resolves to any private address. Callers use it before handing a host to
// something that dials on its own, such as a browser (see issue #241).
func ValidateHost(ctx context.Context, host string, allowPrivate bool) error {
	host = strings.Trim(host, "[]")
	if isBlockedHostname(host) {
		return ErrSSRFBlocked
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return fmt.Errorf("no IP addresses found for %s", host)
	}
	for _, ip := range ips {
		if !allowPrivate && isPrivateIP(ip.IP) {
			return ErrSSRFBlocked
		}
	}
	return nil
}

// guardCloudProxy is a package var so the package's own tests, which point the
// pipeline at loopback httptest servers, can stub it (see TestMain in
// jina_test.go) the same way they redirect JinaReaderURL. Production always
// runs checkCloudProxyTarget.
var guardCloudProxy = checkCloudProxyTarget

// checkCloudProxyTarget refuses to hand pageURL to a cloud extraction proxy (Jina,
// Exa) unless its host resolves only to public addresses. Those tiers never dial
// the target themselves, so the SSRF-safe client cannot protect them; without
// this check a private or internal URL would be sent to a third party. It
// ignores ALLOW_PRIVATE_IPS: that setting permits local fetches, not disclosing
// internal URLs to an external service.
func checkCloudProxyTarget(ctx context.Context, pageURL string) error {
	u, err := url.Parse(pageURL)
	if err != nil || u.Hostname() == "" {
		return contentError(pageURL, "not sent to a cloud proxy: invalid URL")
	}
	if err := ValidateHost(ctx, u.Hostname(), false); err != nil {
		if errors.Is(err, ErrSSRFBlocked) {
			// A skip, not a denial: the direct tiers report the SSRF refusal at
			// dial time, and a private target that fails otherwise (403, 429, ...)
			// must keep its own error kind.
			return contentError(pageURL, "not sent to a cloud proxy: target is not a public host")
		}
		return networkError(pageURL, "", err)
	}
	return nil
}

// GuardArchiveTarget decides whether a URL may be handed to the Internet Archive
// (Save Page Now and the Wayback availability lookup). Tests that target
// loopback httptest servers replace it, as they do JinaReaderURL; production
// always runs checkArchiveTarget.
var GuardArchiveTarget = checkArchiveTarget

// nonPublicNameSuffixes are DNS suffixes reserved for local or internal use.
var nonPublicNameSuffixes = []string{".localhost", ".internal", ".local", ".localdomain", ".lan", ".home.arpa"}

// checkArchiveTarget refuses to send pageURL to the Internet Archive unless its
// host could be a public one. The archive requests dial web.archive.org, not
// the target, so the SSRF-safe client never sees the target host and cannot
// protect it; without this check an internal URL would be handed to a third
// party. It ignores ALLOW_PRIVATE_IPS for the same reason as
// checkCloudProxyTarget. A host that does not resolve at all is allowed: a
// dead public site is exactly what a Wayback lookup is for.
func checkArchiveTarget(ctx context.Context, pageURL string) error {
	u, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil || u.Hostname() == "" {
		return contentError(pageURL, "not sent to the Internet Archive: invalid URL")
	}
	host := strings.ToLower(strings.Trim(u.Hostname(), "[]"))
	notPublic := contentError(pageURL, "not sent to the Internet Archive: target is not a public host")
	if host == "localhost" || (net.ParseIP(host) == nil && !strings.Contains(host, ".")) {
		return notPublic // a single-label name such as "wiki" is never a public host
	}
	for _, suffix := range nonPublicNameSuffixes {
		if strings.HasSuffix(host, suffix) {
			return notPublic
		}
	}
	if err := ValidateHost(ctx, host, false); err != nil {
		switch {
		case errors.Is(err, ErrSSRFBlocked):
			return notPublic
		case isDNSNotFound(err):
			return nil
		}
		return networkError(pageURL, "", err)
	}
	return nil
}
