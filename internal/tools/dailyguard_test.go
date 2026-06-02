package tools

import (
	"testing"
	"time"
)

func TestDailyCallGuardDisabledByDefault(t *testing.T) {
	g := NewDailyCallGuard(0)
	if g.Enabled() {
		t.Fatal("limit 0 must be disabled")
	}
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 1000; i++ {
		if !g.Allow("t1", "u1", now) {
			t.Fatal("disabled guard must always allow")
		}
	}
}

func TestDailyCallGuardEnforcesPerUser(t *testing.T) {
	g := NewDailyCallGuard(3)
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

	// First 3 calls for u1 allowed, 4th denied.
	for i := 0; i < 3; i++ {
		if !g.Allow("t1", "u1", now) {
			t.Fatalf("call %d should be allowed", i+1)
		}
	}
	if g.Allow("t1", "u1", now) {
		t.Fatal("4th call must be denied")
	}

	// A DIFFERENT user has an independent budget.
	if !g.Allow("t1", "u2", now) {
		t.Fatal("a different user must have their own budget")
	}
	// And a different tenant.
	if !g.Allow("t2", "u1", now) {
		t.Fatal("a different tenant must have its own budget")
	}
}

func TestDailyCallGuardResetsAtUTCDay(t *testing.T) {
	g := NewDailyCallGuard(1)
	day1 := time.Date(2026, 6, 2, 23, 59, 0, 0, time.UTC)
	day2 := time.Date(2026, 6, 3, 0, 1, 0, 0, time.UTC)

	if !g.Allow("t1", "u1", day1) {
		t.Fatal("first call day1 allowed")
	}
	if g.Allow("t1", "u1", day1) {
		t.Fatal("second call same day denied")
	}
	if !g.Allow("t1", "u1", day2) {
		t.Fatal("counter must reset on the new UTC day")
	}
}
