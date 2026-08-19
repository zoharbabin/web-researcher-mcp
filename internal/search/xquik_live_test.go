//go:build live

package search

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestXQuikProviderLive(t *testing.T) {
	key := os.Getenv("XQUIK_API_KEY")
	if key == "" {
		t.Skip("XQUIK_API_KEY not set, skipping live integration test")
	}
	provider := NewXQuikProvider(key, Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	results, err := provider.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 3})
	if err != nil {
		t.Fatalf("Web() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Web() returned no results")
	}
	if results[0].URL == "" || results[0].DisplayLink != "x.com" {
		t.Errorf("unexpected first result: %+v", results[0])
	}
}
