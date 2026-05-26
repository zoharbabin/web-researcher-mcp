package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestLensProvider_Patents(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
			w.WriteHeader(405)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(415)
			return
		}

		// Verify request body structure
		body, _ := io.ReadAll(r.Body)
		var reqBody map[string]any
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Errorf("invalid request body: %v", err)
			w.WriteHeader(400)
			return
		}

		// Check query structure exists
		if _, ok := reqBody["query"]; !ok {
			t.Error("missing query in request body")
		}

		response := lensResponse{
			Total: 2,
			Data: []lensDoc{
				{
					LensID:    "119-951-128-551-362",
					Country:   "US",
					DocNumber: "10123456",
					Kind:      "B2",
					Title:     "Video Transcoding Platform",
					Abstract:  "A platform for transcoding video content across multiple formats.",
					FilingDate: "2019-03-15",
					Applicants: []lensParty{{Name: "Kaltura Inc"}},
					LegalStatus: lensLegal{Granted: true, GrantDate: "2021-11-30"},
				},
				{
					LensID:    "220-062-239-662-473",
					Country:   "WO",
					DocNumber: "2020123456",
					Kind:      "A1",
					Title:     "Adaptive Streaming Method",
					Abstract:  "Method for adaptive bitrate streaming with ML-based prediction.",
					FilingDate: "2020-01-10",
					Applicants: []lensParty{{Name: "Kaltura Inc"}},
					LegalStatus: lensLegal{Granted: false},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	provider := NewLensProvider("test-token", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Patents(context.Background(), PatentSearchParams{
		Query:      "video transcoding",
		Assignee:   "Kaltura",
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Title != "Video Transcoding Platform" {
		t.Errorf("unexpected title: %s", results[0].Title)
	}
	if results[0].Number != "US10123456" {
		t.Errorf("unexpected number: %s", results[0].Number)
	}
	if results[0].Assignee != "Kaltura Inc" {
		t.Errorf("unexpected assignee: %s", results[0].Assignee)
	}
	if results[0].Granted != "2021-11-30" {
		t.Errorf("unexpected grant date: %s", results[0].Granted)
	}

	// Second result should have no grant date
	if results[1].Granted != "" {
		t.Errorf("expected empty grant date for WO application, got: %s", results[1].Granted)
	}
	if results[1].Number != "WO2020123456" {
		t.Errorf("unexpected number: %s", results[1].Number)
	}
}

func TestLensProvider_QueryConstruction(t *testing.T) {
	t.Parallel()

	provider := NewLensProvider("token", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	tests := []struct {
		name   string
		params PatentSearchParams
		check  func(t *testing.T, body []byte)
	}{
		{
			name:   "simple query",
			params: PatentSearchParams{Query: "video encoding", NumResults: 3},
			check: func(t *testing.T, body []byte) {
				var req map[string]any
				json.Unmarshal(body, &req)
				if size, _ := req["size"].(float64); size != 3 {
					t.Errorf("expected size 3, got %v", size)
				}
			},
		},
		{
			name:   "with jurisdiction filter",
			params: PatentSearchParams{Query: "AI", PatentOffice: "EP", NumResults: 5},
			check: func(t *testing.T, body []byte) {
				s := string(body)
				if !strings.Contains(s, `"jurisdiction"`) {
					t.Error("expected jurisdiction filter in query")
				}
				if !strings.Contains(s, `"EP"`) {
					t.Error("expected EP value in query")
				}
			},
		},
		{
			name:   "with date range",
			params: PatentSearchParams{Query: "ML", YearFrom: 2020, YearTo: 2024, NumResults: 5},
			check: func(t *testing.T, body []byte) {
				s := string(body)
				if !strings.Contains(s, "2020-01-01") {
					t.Error("expected year_from in range query")
				}
				if !strings.Contains(s, "2024-12-31") {
					t.Error("expected year_to in range query")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := provider.buildQuery(tt.params)
			tt.check(t, body)
		})
	}
}

func TestLensProvider_RateLimit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	provider := NewLensProvider("token", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	_, err := provider.Patents(context.Background(), PatentSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected error for rate limit")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

func TestLensProvider_AuthFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	provider := NewLensProvider("bad-token", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	_, err := provider.Patents(context.Background(), PatentSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected error for auth failure")
	}
	if !strings.Contains(err.Error(), "authentication") {
		t.Errorf("expected auth error, got: %v", err)
	}
}

func TestLensProvider_Metadata(t *testing.T) {
	t.Parallel()

	provider := NewLensProvider("token", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	meta := provider.Metadata()
	if !meta.MatchesRegion("US") {
		t.Error("expected worldwide provider to match US")
	}
	if !meta.MatchesRegion("JP") {
		t.Error("expected worldwide provider to match JP")
	}
	if !meta.HasCapability("scholarly") {
		t.Error("expected scholarly capability")
	}
}
