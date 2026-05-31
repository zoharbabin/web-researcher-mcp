//go:build live

// Live external-API integration tests. Excluded from the default suite because
// they depend on third-party endpoints (ops.epo.org) whose latency/availability
// makes them non-deterministic — they must never gate CI. Run on demand with
// `make test-live` (requires the relevant provider credentials in the env).
package search

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestEPOLiveIntegration(t *testing.T) {
	key := os.Getenv("EPO_OPS_CONSUMER_KEY")
	secret := os.Getenv("EPO_OPS_CONSUMER_SECRET")
	if key == "" || secret == "" {
		t.Skip("EPO_OPS_CONSUMER_KEY and EPO_OPS_CONSUMER_SECRET not set")
	}

	provider := NewEPOProvider(key, secret, Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 3, ResetTimeout: 30}),
	})

	t.Run("Apple LLM patents", func(t *testing.T) {
		results, err := provider.Patents(context.Background(), PatentSearchParams{
			Query:      "language model",
			Assignee:   "Apple",
			YearFrom:   2024,
			NumResults: 5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results, got none")
		}
		fmt.Printf("  Found %d results:\n", len(results))
		for i, r := range results {
			fmt.Printf("    %d. [%s] %s\n", i+1, r.Number, r.Title)
			fmt.Printf("       Assignee: %s | Filed: %s | Published: %s\n", r.Assignee, r.Filed, r.Granted)
			if r.Abstract != "" {
				abs := r.Abstract
				if len(abs) > 100 {
					abs = abs[:100] + "..."
				}
				fmt.Printf("       Abstract: %s\n", abs)
			}
		}
	})

	t.Run("EP office filter", func(t *testing.T) {
		results, err := provider.Patents(context.Background(), PatentSearchParams{
			Query:        "neural network",
			PatentOffice: "EP",
			YearFrom:     2023,
			NumResults:   3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		fmt.Printf("  EP-only results: %d\n", len(results))
		for i, r := range results {
			fmt.Printf("    %d. [%s] %s\n", i+1, r.Number, r.Title)
		}
	})

	t.Run("Inventor search", func(t *testing.T) {
		results, err := provider.Patents(context.Background(), PatentSearchParams{
			Inventor:   "Hinton",
			YearFrom:   2020,
			NumResults: 3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		fmt.Printf("  Inventor 'Hinton' results: %d\n", len(results))
		for i, r := range results {
			fmt.Printf("    %d. [%s] %s (%s)\n", i+1, r.Number, r.Title, r.Assignee)
		}
	})
}
