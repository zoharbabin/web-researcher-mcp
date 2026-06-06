package session

import "testing"

func TestAggregateOutcomesEmpty(t *testing.T) {
	p, s := AggregateOutcomes(nil)
	if p != nil || s != nil {
		t.Error("empty outcomes should yield nil, nil")
	}
}

func TestAggregateOutcomesPatternThreshold(t *testing.T) {
	// 2 auth failures → below threshold, no pattern. 3 blocked → pattern.
	outcomes := []OutcomeEvent{
		{Provider: "brave", Success: true},
		{ErrorKind: "auth_required", URL: "https://a.com"},
		{ErrorKind: "auth_required", URL: "https://b.com"},
		{ErrorKind: "blocked", URL: "https://x.com"},
		{ErrorKind: "blocked", URL: "https://y.com"},
		{ErrorKind: "blocked", URL: "https://z.com"},
	}
	patterns, _ := AggregateOutcomes(outcomes)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern (blocked), got %d: %+v", len(patterns), patterns)
	}
	if patterns[0].Kind != "blocked" || patterns[0].Count != 3 {
		t.Errorf("unexpected pattern: %+v", patterns[0])
	}
	if patterns[0].Suggestion == "" {
		t.Error("pattern should carry a session-level suggestion")
	}
	if len(patterns[0].AffectedURLs) != 3 {
		t.Errorf("expected 3 affected URLs, got %v", patterns[0].AffectedURLs)
	}
}

func TestAggregateOutcomesProviderStats(t *testing.T) {
	outcomes := []OutcomeEvent{
		{Provider: "brave", Success: true},
		{Provider: "brave", Success: true},
		{Provider: "searxng", Success: false, ErrorKind: "rate_limited"},
		{Provider: "searxng", Success: true},
	}
	_, stats := AggregateOutcomes(outcomes)
	if stats["brave"].Attempts != 2 || stats["brave"].Successes != 2 {
		t.Errorf("brave stats wrong: %+v", stats["brave"])
	}
	if stats["searxng"].Attempts != 2 || stats["searxng"].Successes != 1 {
		t.Errorf("searxng stats wrong: %+v", stats["searxng"])
	}
}

func TestAggregateOutcomesOrderingByCount(t *testing.T) {
	outcomes := []OutcomeEvent{}
	for i := 0; i < 5; i++ {
		outcomes = append(outcomes, OutcomeEvent{ErrorKind: "blocked"})
	}
	for i := 0; i < 3; i++ {
		outcomes = append(outcomes, OutcomeEvent{ErrorKind: "auth_required"})
	}
	patterns, _ := AggregateOutcomes(outcomes)
	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(patterns))
	}
	if patterns[0].Kind != "blocked" || patterns[1].Kind != "auth_required" {
		t.Errorf("patterns should be ordered by descending count: %+v", patterns)
	}
}

func TestAppendOutcomeBounded(t *testing.T) {
	sess := &Session{}
	for i := 0; i < MaxOutcomes+50; i++ {
		appendOutcome(sess, OutcomeEvent{Provider: "p", Success: true})
	}
	if len(sess.Outcomes) != MaxOutcomes {
		t.Errorf("outcomes should be capped at %d, got %d", MaxOutcomes, len(sess.Outcomes))
	}
}

func TestAggregateOutcomesAffectedURLsCapped(t *testing.T) {
	var outcomes []OutcomeEvent
	for i := 0; i < 10; i++ {
		outcomes = append(outcomes, OutcomeEvent{ErrorKind: "blocked", URL: "https://site.com/" + string(rune('a'+i))})
	}
	patterns, _ := AggregateOutcomes(outcomes)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern")
	}
	if len(patterns[0].AffectedURLs) > maxAffectedURLs {
		t.Errorf("affected URLs not capped: %d", len(patterns[0].AffectedURLs))
	}
}
