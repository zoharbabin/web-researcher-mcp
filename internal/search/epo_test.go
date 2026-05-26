package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

const testEPOTokenResponse = `{"access_token":"test-bearer-token","token_type":"BearerToken","expires_in":1200}`

const testEPOSearchResponse = `<?xml version="1.0" encoding="UTF-8"?>
<ops:world-patent-data xmlns:ops="http://ops.epo.org" xmlns="http://www.epo.org/exchange">
  <ops:biblio-search total-result-count="2">
    <ops:search-result>
      <exchange-documents>
        <exchange-document country="EP" doc-number="3456789" kind="A1">
          <bibliographic-data>
            <invention-title lang="en">Video Content Delivery Network</invention-title>
            <parties>
              <applicants>
                <applicant>
                  <applicant-name><name>KALTURA INC</name></applicant-name>
                </applicant>
              </applicants>
              <inventors>
                <inventor>
                  <inventor-name><name>John Smith</name></inventor-name>
                </inventor>
              </inventors>
            </parties>
            <application-reference>
              <document-id>
                <country>EP</country>
                <doc-number>3456789</doc-number>
                <kind>A1</kind>
                <date>20190415</date>
              </document-id>
            </application-reference>
            <publication-reference>
              <document-id>
                <country>EP</country>
                <doc-number>3456789</doc-number>
                <kind>A1</kind>
                <date>20210922</date>
              </document-id>
            </publication-reference>
          </bibliographic-data>
          <abstract lang="en"><p>A system for delivering video content via a CDN with adaptive bitrate.</p></abstract>
        </exchange-document>
      </exchange-documents>
    </ops:search-result>
  </ops:biblio-search>
</ops:world-patent-data>`

func TestEPOProvider_Patents(t *testing.T) {
	t.Parallel()

	tokenCalled := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "accesstoken") {
			tokenCalled.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(testEPOTokenResponse))
			return
		}
		if strings.Contains(r.URL.Path, "published-data/search") {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-bearer-token" {
				w.WriteHeader(401)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(testEPOSearchResponse))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	provider := NewEPOProvider("test-key", "test-secret", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)
	provider.SetTokenURL(srv.URL + "/accesstoken")

	results, err := provider.Patents(context.Background(), PatentSearchParams{
		Query:      "video delivery",
		Assignee:   "Kaltura",
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].Title != "Video Content Delivery Network" {
		t.Errorf("unexpected title: %s", results[0].Title)
	}
	if results[0].Number != "EP3456789" {
		t.Errorf("unexpected number: %s", results[0].Number)
	}
	if results[0].Assignee != "KALTURA INC" {
		t.Errorf("unexpected assignee: %s", results[0].Assignee)
	}
	if results[0].Filed != "2019-04-15" {
		t.Errorf("unexpected filed date: %s", results[0].Filed)
	}
	if results[0].Granted != "2021-09-22" {
		t.Errorf("unexpected granted date: %s", results[0].Granted)
	}

	if tokenCalled.Load() != 1 {
		t.Errorf("expected 1 token call, got %d", tokenCalled.Load())
	}
}

func TestEPOProvider_TokenReuse(t *testing.T) {
	t.Parallel()

	tokenCalled := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "accesstoken") {
			tokenCalled.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(testEPOTokenResponse))
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(testEPOSearchResponse))
	}))
	defer srv.Close()

	provider := NewEPOProvider("key", "secret", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)
	provider.SetTokenURL(srv.URL + "/accesstoken")

	// Call twice — token should be reused
	for i := 0; i < 3; i++ {
		_, err := provider.Patents(context.Background(), PatentSearchParams{Query: "test"})
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}

	if tokenCalled.Load() != 1 {
		t.Errorf("expected token to be called once (reused), got %d", tokenCalled.Load())
	}
}

func TestEPOProvider_ConcurrentTokenRefresh(t *testing.T) {
	t.Parallel()

	tokenCalled := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "accesstoken") {
			tokenCalled.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(testEPOTokenResponse))
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(testEPOSearchResponse))
	}))
	defer srv.Close()

	provider := NewEPOProvider("key", "secret", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)
	provider.SetTokenURL(srv.URL + "/accesstoken")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = provider.Patents(context.Background(), PatentSearchParams{Query: "test"})
		}()
	}
	wg.Wait()

	// With double-check locking, token should only be fetched a small number of times
	if tokenCalled.Load() > 3 {
		t.Errorf("expected few token calls with double-check locking, got %d", tokenCalled.Load())
	}
}

func TestEPOProvider_QuotaExceeded(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "accesstoken") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(testEPOTokenResponse))
			return
		}
		w.Header().Set("X-Rejection-Reason", "IndividualQuotaPerHour")
		w.WriteHeader(403)
	}))
	defer srv.Close()

	provider := NewEPOProvider("key", "secret", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)
	provider.SetTokenURL(srv.URL + "/accesstoken")

	_, err := provider.Patents(context.Background(), PatentSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected error for quota exceeded")
	}
	if !strings.Contains(err.Error(), "quota") {
		t.Errorf("expected quota error, got: %v", err)
	}
}

func TestEPOProvider_CQLConstruction(t *testing.T) {
	t.Parallel()

	provider := NewEPOProvider("key", "secret", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	tests := []struct {
		name   string
		params PatentSearchParams
		want   string
	}{
		{
			name:   "multi-word query splits into keywords",
			params: PatentSearchParams{Query: "video encoding"},
			want:   `txt=video AND txt=encoding`,
		},
		{
			name:   "single word query",
			params: PatentSearchParams{Query: "AI", Assignee: "Google", Inventor: "Smith"},
			want:   `txt=AI AND pa="Google" AND in="Smith"`,
		},
		{
			name:   "with date range and office",
			params: PatentSearchParams{Query: "network", PatentOffice: "EP", YearFrom: 2020, YearTo: 2024},
			want:   `txt=network AND pn=EP AND pd>=2020 AND pd<=2024`,
		},
		{
			name:   "multi-word with all fields",
			params: PatentSearchParams{Query: "language model inference", Assignee: "Apple", PatentOffice: "EP"},
			want:   `txt=language AND txt=model AND txt=inference AND pa="Apple" AND pn=EP`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := provider.buildCQL(tt.params)
			if got != tt.want {
				t.Errorf("buildCQL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEPOProvider_Metadata(t *testing.T) {
	t.Parallel()

	provider := NewEPOProvider("key", "secret", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	meta := provider.Metadata()
	if !meta.MatchesRegion("US") {
		t.Error("expected worldwide provider to match US")
	}
	if !meta.MatchesRegion("EP") {
		t.Error("expected worldwide provider to match EP")
	}
	if !meta.MatchesRegion("") {
		t.Error("expected to match empty region")
	}
}
