package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestUSPTOProvider_Patents(t *testing.T) {
	t.Parallel()

	response := usptoResponse{
		Count: 2,
		PatentFileWrapperDataBag: []usptoFileWrapperDataBag{
			{
				ApplicationNumberText: "16123456",
				ApplicationMetaData: usptoApplicationMetaData{
					InventionTitle:                   "Method for Video Processing",
					PatentNumber:                     "11234567",
					FirstApplicantName:               "Kaltura Inc",
					FirstInventorName:                "John Smith",
					FilingDate:                       "2020-03-15",
					GrantDate:                        "2023-01-10",
					ApplicationStatusDescriptionText: "Patented Case",
					CPCClassificationBag:             []string{"H04N21/234"},
				},
			},
			{
				ApplicationNumberText: "15987654",
				ApplicationMetaData: usptoApplicationMetaData{
					InventionTitle:                   "Cloud Media Encoding System",
					PatentNumber:                     "10987654",
					FirstApplicantName:               "Kaltura Inc",
					FirstInventorName:                "Jane Doe",
					FilingDate:                       "2019-06-01",
					GrantDate:                        "2022-05-20",
					ApplicationStatusDescriptionText: "Patented Case",
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != "test-key" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Query().Get("q") == "" {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	provider := NewUSPTOProvider("test-key", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Patents(context.Background(), PatentSearchParams{
		Query:      "video processing",
		Assignee:   "Kaltura",
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Title != "Method for Video Processing" {
		t.Errorf("unexpected title: %s", results[0].Title)
	}
	if results[0].Number != "US11234567" {
		t.Errorf("unexpected number: %s", results[0].Number)
	}
	if results[0].Assignee != "Kaltura Inc" {
		t.Errorf("unexpected assignee: %s", results[0].Assignee)
	}
	if results[0].Inventor != "John Smith" {
		t.Errorf("unexpected inventor: %s", results[0].Inventor)
	}
	if results[0].Filed != "2020-03-15" {
		t.Errorf("unexpected filed date: %s", results[0].Filed)
	}
	if results[0].Status != "Patented Case" {
		t.Errorf("unexpected status: %s", results[0].Status)
	}
}

func TestUSPTOProvider_AssigneeFromAssignments(t *testing.T) {
	t.Parallel()

	response := usptoResponse{
		Count: 1,
		PatentFileWrapperDataBag: []usptoFileWrapperDataBag{
			{
				ApplicationNumberText: "16999999",
				ApplicationMetaData: usptoApplicationMetaData{
					InventionTitle: "Assigned Patent",
					PatentNumber:   "11999999",
					FilingDate:     "2021-01-01",
				},
				AssignmentBag: []usptoAssignment{
					{
						ConveyanceText: "ASSIGNMENT OF ASSIGNORS INTEREST",
						AssigneeBag: []usptoAssigneeInfo{
							{AssigneeNameText: "Kaltura Inc."},
						},
					},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	provider := NewUSPTOProvider("test-key", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Patents(context.Background(), PatentSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Assignee != "Kaltura Inc." {
		t.Errorf("expected assignee from assignments, got: %s", results[0].Assignee)
	}
}

func TestUSPTOProvider_RegionFilter(t *testing.T) {
	t.Parallel()

	provider := NewUSPTOProvider("test-key", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	results, err := provider.Patents(context.Background(), PatentSearchParams{
		Query:        "video",
		PatentOffice: "EP",
	})
	if err != nil {
		t.Fatalf("unexpected error for EP filter: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for EP filter, got %d", len(results))
	}
}

func TestUSPTOProvider_RateLimit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	provider := NewUSPTOProvider("test-key", Deps{
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

func TestUSPTOProvider_QueryConstruction(t *testing.T) {
	t.Parallel()

	provider := NewUSPTOProvider("key", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	tests := []struct {
		name   string
		params PatentSearchParams
		want   string
	}{
		{
			name:   "simple query",
			params: PatentSearchParams{Query: "video encoding"},
			want:   "applicationMetaData.inventionTitle:(video encoding)",
		},
		{
			name:   "with assignee",
			params: PatentSearchParams{Query: "video", Assignee: "Kaltura"},
			want:   `applicationMetaData.inventionTitle:(video) AND applicationMetaData.firstApplicantName:Kaltura`,
		},
		{
			name:   "with year range",
			params: PatentSearchParams{Query: "AI", YearFrom: 2020, YearTo: 2024},
			want:   `applicationMetaData.inventionTitle:(AI) AND applicationMetaData.filingDate:[2020-01-01 TO 2024-12-31]`,
		},
		{
			name:   "assignee only",
			params: PatentSearchParams{Assignee: "Google"},
			want:   `applicationMetaData.firstApplicantName:Google`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := provider.buildQuery(tt.params)
			if got != tt.want {
				t.Errorf("buildQuery() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUSPTOProvider_Metadata(t *testing.T) {
	t.Parallel()

	provider := NewUSPTOProvider("key", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	meta := provider.Metadata()
	if !meta.MatchesRegion("US") {
		t.Error("expected to match US region")
	}
	if meta.MatchesRegion("EP") {
		t.Error("expected not to match EP region")
	}
	if !meta.MatchesRegion("") {
		t.Error("expected to match empty region (all)")
	}
}
