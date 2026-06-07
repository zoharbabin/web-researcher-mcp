package search

import (
	"context"
	"testing"
)

// TestRouterHealth_AllClosedHealthy: fresh breakers → all available → "healthy".
func TestRouterHealth_AllClosedHealthy(t *testing.T) {
	r := NewRouter(map[string]Provider{
		"google": &successProvider{name: "google"},
		"brave":  &successProvider{name: "brave"},
	}, RouterConfig{Routing: RoutingConfig{Default: []string{"google", "brave"}}})

	h := r.Health()
	if h.Status != "healthy" {
		t.Errorf("Status = %q, want healthy", h.Status)
	}
	if len(h.Providers) != 2 {
		t.Fatalf("Providers len = %d, want 2", len(h.Providers))
	}
	// Deterministic order (type, name): brave before google.
	if h.Providers[0].Name != "brave" || h.Providers[1].Name != "google" {
		t.Errorf("provider order = %v", []string{h.Providers[0].Name, h.Providers[1].Name})
	}
	for _, p := range h.Providers {
		if !p.Available || p.Breaker != "closed" {
			t.Errorf("provider %s: breaker=%q available=%v, want closed/true", p.Name, p.Breaker, p.Available)
		}
	}
}

// TestRouterHealth_Degraded: one breaker open while another is closed → "degraded".
func TestRouterHealth_Degraded(t *testing.T) {
	r := NewRouter(map[string]Provider{
		"failing": &failingProvider{name: "failing"},
		"ok":      &successProvider{name: "ok"},
	}, RouterConfig{Routing: RoutingConfig{Default: []string{"failing", "ok"}}})

	// Trip the failing provider's breaker (threshold 3).
	for i := 0; i < 3; i++ {
		_, _ = r.Web(context.Background(), WebSearchParams{Query: "warm"})
	}

	h := r.Health()
	if h.Status != "degraded" {
		t.Errorf("Status = %q, want degraded", h.Status)
	}
	var failing ProviderHealth
	for _, p := range h.Providers {
		if p.Name == "failing" {
			failing = p
		}
	}
	if failing.Breaker != "open" || failing.Available {
		t.Errorf("failing breaker = %q available = %v, want open/false", failing.Breaker, failing.Available)
	}
}

// TestRouterHealth_Empty: no providers → empty snapshot reports "healthy"
// (nothing to be unhealthy about — single-provider/no-routing deployments).
func TestRouterHealth_Empty(t *testing.T) {
	r := NewRouter(map[string]Provider{}, RouterConfig{})
	h := r.Health()
	if h.Status != "healthy" {
		t.Errorf("Status = %q, want healthy", h.Status)
	}
	if len(h.Providers) != 0 {
		t.Errorf("Providers len = %d, want 0", len(h.Providers))
	}
}

// TestRouterHealth_CoversAllFamilies: web, patent, and academic breakers all
// appear, each tagged with its capability family.
func TestRouterHealth_CoversAllFamilies(t *testing.T) {
	r := NewRouter(
		map[string]Provider{"google": &successProvider{name: "google"}},
		RouterConfig{
			Routing: RoutingConfig{Default: []string{"google"}},
			PatentProviders: map[string]PatentProvider{
				"epo": &mockPatentProvider{name: "epo", meta: ProviderMeta{}},
			},
			AcademicProviders: map[string]AcademicProvider{
				"openalex": &mockAcademicProvider{name: "openalex"},
			},
		})

	h := r.Health()
	types := map[string]string{}
	for _, p := range h.Providers {
		types[p.Name] = p.Type
	}
	if types["google"] != "web" || types["epo"] != "patent" || types["openalex"] != "academic" {
		t.Errorf("family tags = %v", types)
	}
}
