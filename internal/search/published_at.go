package search

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// normalizePublishedAt converts the assorted date strings news providers emit
// into a single ISO-8601 (RFC3339) timestamp so callers can sort and freshness-
// check programmatically (#234). Providers hand back wildly different shapes:
// RFC3339 (Exa), RFC1123 (Tavily), "Jun 5, 2026" (Serper/SearchAPI), Open Graph
// times (Google), and relative ages like "3 days ago" / "2h" (Brave).
//
// A parseable value returns its RFC3339 form (UTC). An unparseable value returns
// "" so the field is dropped by NewsResult's omitempty rather than handed back as
// an un-sortable relative string. `now` is the reference for relative ages —
// callers pass time.Now(); tests pass a fixed instant.
func normalizePublishedAt(raw string, now time.Time) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// Absolute formats, most-specific first. RFC3339 with fractional seconds and
	// the common publisher/CSE layouts are all covered.
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		time.RFC1123Z,
		time.RFC1123,
		"2006-01-02 15:04:05",
		"2006-01-02",
		"Jan 2, 2006",
		"January 2, 2006",
		"02 Jan 2006",
		"01/02/2006",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}

	// Relative ages: "3 days ago", "2 hours ago", "1 week ago", and the compact
	// "3d" / "2h" / "5m" forms Brave returns.
	if t, ok := parseRelativeAge(s, now); ok {
		return t.UTC().Format(time.RFC3339)
	}
	return ""
}

var (
	relativeWordRe    = regexp.MustCompile(`(?i)^(\d+)\s*(second|minute|hour|day|week|month|year)s?\s+ago$`)
	relativeCompactRe = regexp.MustCompile(`(?i)^(\d+)\s*(s|m|h|d|w|mo|y)$`)
)

// parseRelativeAge resolves "3 days ago" / "2h" style strings against now.
func parseRelativeAge(s string, now time.Time) (time.Time, bool) {
	if m := relativeWordRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return subtractUnit(now, n, strings.ToLower(m[2])), true
	}
	if m := relativeCompactRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		// Compact units overlap word units except "m" (minute) vs "mo" (month).
		unit := map[string]string{
			"s": "second", "m": "minute", "h": "hour", "d": "day",
			"w": "week", "mo": "month", "y": "year",
		}[strings.ToLower(m[2])]
		return subtractUnit(now, n, unit), true
	}
	return time.Time{}, false
}

func subtractUnit(now time.Time, n int, unit string) time.Time {
	switch unit {
	case "second":
		return now.Add(-time.Duration(n) * time.Second)
	case "minute":
		return now.Add(-time.Duration(n) * time.Minute)
	case "hour":
		return now.Add(-time.Duration(n) * time.Hour)
	case "day":
		return now.AddDate(0, 0, -n)
	case "week":
		return now.AddDate(0, 0, -7*n)
	case "month":
		return now.AddDate(0, -n, 0)
	case "year":
		return now.AddDate(-n, 0, 0)
	}
	return now
}
