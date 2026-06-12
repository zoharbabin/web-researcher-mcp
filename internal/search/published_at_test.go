package search

import (
	"testing"
	"time"
)

func TestNormalizePublishedAt(t *testing.T) {
	// Fixed reference for relative-age math: 2026-06-12T12:00:00Z.
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"rfc3339", "2026-06-05T08:30:00Z", "2026-06-05T08:30:00Z"},
		{"rfc3339-offset", "2026-06-05T08:30:00+02:00", "2026-06-05T06:30:00Z"},
		{"rfc3339-nano-zulu", "2026-06-05T08:30:00.000Z", "2026-06-05T08:30:00Z"},
		{"rfc1123", "Fri, 05 Jun 2026 08:30:00 GMT", "2026-06-05T08:30:00Z"},
		{"date-only", "2026-06-05", "2026-06-05T00:00:00Z"},
		{"us-month", "Jun 5, 2026", "2026-06-05T00:00:00Z"},
		{"long-month", "January 2, 2026", "2026-01-02T00:00:00Z"},
		{"relative-days", "3 days ago", "2026-06-09T12:00:00Z"},
		{"relative-hours", "2 hours ago", "2026-06-12T10:00:00Z"},
		{"relative-week", "1 week ago", "2026-06-05T12:00:00Z"},
		{"compact-days", "3d", "2026-06-09T12:00:00Z"},
		{"compact-hours", "2h", "2026-06-12T10:00:00Z"},
		{"compact-minutes", "5m", "2026-06-12T11:55:00Z"},
		{"compact-month", "1mo", "2026-05-12T12:00:00Z"},
		{"unparseable", "sometime last spring", ""},
		{"garbage", "n/a", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizePublishedAt(c.in, now); got != c.want {
				t.Errorf("normalizePublishedAt(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
