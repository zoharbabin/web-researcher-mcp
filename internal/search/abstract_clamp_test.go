package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// longCJKAbstract is over the 500-byte abstract cap, and 500 is not a multiple
// of 3, so a raw byte cut lands inside a character.
var longCJKAbstract = strings.Repeat("日本語", 200)

// lensAbstractFor returns the abstract the Lens provider yields for a stored abstract.
func lensAbstractFor(t *testing.T, stored string) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		abstract, _ := json.Marshal([]map[string]string{{"text": stored, "lang": "en"}})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(lensResponse{
			Total: 1,
			Data: []lensDoc{{
				LensID:      "119-951-128-551-362",
				Country:     "US",
				DocNumber:   json.RawMessage(`"10123456"`),
				Kind:        "B2",
				RawAbstract: abstract,
			}},
		})
	}))
	defer srv.Close()

	provider := NewLensProvider("test-token", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Patents(context.Background(), PatentSearchParams{Query: "q", NumResults: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	return results[0].Abstract
}

// epoAbstractFor returns the abstract the EPO provider yields for a stored abstract.
func epoAbstractFor(t *testing.T, stored string) string {
	t.Helper()

	body := strings.Replace(testEPOSearchResponse,
		"A system for delivering video content via a CDN with adaptive bitrate.",
		stored, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "accesstoken") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(testEPOTokenResponse))
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	provider := NewEPOProvider("test-key", "test-secret", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)
	provider.SetTokenURL(srv.URL + "/accesstoken")

	results, err := provider.Patents(context.Background(), PatentSearchParams{Query: "q", NumResults: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	return results[0].Abstract
}

func TestPatentAbstractClamp(t *testing.T) {
	t.Parallel()

	providers := map[string]func(*testing.T, string) string{
		"lens": lensAbstractFor,
		"epo":  epoAbstractFor,
	}
	for name, abstractFor := range providers {
		t.Run(name+"/never splits a character", func(t *testing.T) {
			t.Parallel()
			got := abstractFor(t, longCJKAbstract)
			if !utf8.ValidString(got) {
				t.Errorf("abstract is not valid UTF-8: %q", got)
			}
			if !strings.HasSuffix(got, "...") || len(got) > 503 {
				t.Errorf("unexpected abstract shape (len %d): %q", len(got), got)
			}
		})
		t.Run(name+"/cuts on a word boundary", func(t *testing.T) {
			t.Parallel()
			// 9-byte words: the 500-byte limit falls inside a word.
			got := abstractFor(t, strings.Repeat("alphabet ", 100))
			if !strings.HasSuffix(got, "alphabet...") || len(got) > 503 {
				t.Errorf("abstract not cut on a word boundary (len %d): %q", len(got), got)
			}
		})
	}
}
