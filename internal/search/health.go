package search

import (
	"sort"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// ProviderHealth is the live health of one routed provider: its name, the
// capability family it serves (web/patent/academic), and its circuit-breaker
// state. Operator/debug data (issue #81) — never content. Carries no counts,
// URLs, or error bodies.
type ProviderHealth struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Breaker   string `json:"breaker"`   // closed | open | half-open
	Available bool   `json:"available"` // breaker != open
}

// HealthSnapshot is the Router's live provider/breaker view. The aggregate
// Status is tri-state: "healthy" (all available), "degraded" (some open, some
// available), or "unhealthy" (all open / none available). An empty provider set
// reports "healthy" (nothing to be unhealthy about — single-provider/no-routing
// deployments simply have no breaker ladder to observe).
type HealthSnapshot struct {
	Status    string           `json:"status"`
	Providers []ProviderHealth `json:"providers"`
}

func breakerStateString(s circuit.State) string {
	switch s {
	case circuit.StateOpen:
		return "open"
	case circuit.StateHalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

// Health returns a live snapshot of every routed provider's circuit-breaker
// state across all three capability families the Router covers (web, patent,
// academic). It reaches into breaker state through the breaker's own State()
// accessor only — no internal counts are exposed. Deterministically ordered by
// (type, name) so the snapshot is stable across calls.
func (r *Router) Health() HealthSnapshot {
	r.mu.RLock()
	out := make([]ProviderHealth, 0, len(r.breakers)+len(r.patentBreakers)+len(r.academicBreakers))
	collect := func(typ string, breakers map[string]*circuit.Breaker) {
		for name, b := range breakers {
			state := breakerStateString(b.State())
			out = append(out, ProviderHealth{
				Name:      name,
				Type:      typ,
				Breaker:   state,
				Available: state != "open",
			})
		}
	}
	collect("web", r.breakers)
	collect("patent", r.patentBreakers)
	collect("academic", r.academicBreakers)
	r.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Name < out[j].Name
	})

	return HealthSnapshot{Status: aggregateStatus(out), Providers: out}
}

// aggregateStatus reduces per-provider availability to the tri-state rollup.
func aggregateStatus(providers []ProviderHealth) string {
	if len(providers) == 0 {
		return "healthy"
	}
	available := 0
	for _, p := range providers {
		if p.Available {
			available++
		}
	}
	switch {
	case available == len(providers):
		return "healthy"
	case available == 0:
		return "unhealthy"
	default:
		return "degraded"
	}
}
