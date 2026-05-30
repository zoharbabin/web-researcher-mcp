package scraper

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
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

func NewSSRFSafeClient(allowPrivate bool) *http.Client {
	return &http.Client{
		Transport: newSSRFSafeTransport(allowPrivate),
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
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
