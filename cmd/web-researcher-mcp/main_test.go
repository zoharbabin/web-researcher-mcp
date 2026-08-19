package main

import (
	"testing"
	"time"
)

// TestOsintClientDedicatedTimeout proves ctLogResolver and archiveResolver
// share the same dedicated-timeout client (#592) rather than one of them
// falling back to the 30s shared SSRF client that crt.sh/Wayback CDX both
// regularly exceed under documented upstream latency.
func TestOsintClientDedicatedTimeout(t *testing.T) {
	c := osintClient(false)
	if c.Timeout != osintClientTimeout {
		t.Fatalf("osintClient timeout = %v, want %v", c.Timeout, osintClientTimeout)
	}
	if c.Timeout == 30*time.Second {
		t.Fatalf("osintClient must not use the default 30s shared SSRF client timeout")
	}
}
