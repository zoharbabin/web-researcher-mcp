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

// TestUSPTOProvider_AbstractGenuinelyUnavailable documents the #635 contract:
// USPTO's PEDS/ODP applications-search response (verified live against
// api.uspto.gov, 2026-08-19) carries no abstract text anywhere — it's a
// prosecution-history dataset, not a full-text one. Abstract stays "" rather
// than fabricating a value from some other field; this locks that in against
// a future edit accidentally back-filling it from e.g. the invention title.
func TestUSPTOProvider_AbstractGenuinelyUnavailable(t *testing.T) {
	t.Parallel()

	response := usptoResponse{
		Count: 1,
		PatentFileWrapperDataBag: []usptoFileWrapperDataBag{
			{
				ApplicationNumberText: "16123456",
				ApplicationMetaData: usptoApplicationMetaData{
					InventionTitle: "Method for Video Processing",
					PatentNumber:   "11234567",
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	provider := NewUSPTOProvider("test-key", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Patents(context.Background(), PatentSearchParams{Query: "video processing", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Abstract != "" {
		t.Errorf("expected Abstract to stay empty (USPTO's API has no abstract field), got %q", results[0].Abstract)
	}
}

// TestUSPTOProvider_CapsResults verifies the defensive result cap: when the API
// returns more rows than requested (the `rows` param is not always honored), the
// provider slices down to NumResults to match every other provider's contract.
func TestUSPTOProvider_CapsResults(t *testing.T) {
	t.Parallel()

	// Build a response with 10 records.
	var bag []usptoFileWrapperDataBag
	for i := 0; i < 10; i++ {
		bag = append(bag, usptoFileWrapperDataBag{
			ApplicationNumberText: "16000000",
			ApplicationMetaData:   usptoApplicationMetaData{InventionTitle: "Patent", PatentNumber: "11000000"},
		})
	}
	response := usptoResponse{Count: 10, PatentFileWrapperDataBag: bag}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	provider := NewUSPTOProvider("test-key", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Patents(context.Background(), PatentSearchParams{Query: "x", NumResults: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected results capped to 3, got %d", len(results))
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

// TestUSPTOProvider_YearRangeFilter proves the #528 fix: year_from/year_to is
// enforced client-side on the Filed date, since USPTO's PEDS API has no
// native filing-date range query parameter. Without the filter, all 3 results
// (spanning 2015-2023) would come back regardless of the requested range.
func TestUSPTOProvider_YearRangeFilter(t *testing.T) {
	t.Parallel()

	response := usptoResponse{
		Count: 3,
		PatentFileWrapperDataBag: []usptoFileWrapperDataBag{
			{ApplicationMetaData: usptoApplicationMetaData{InventionTitle: "Old", PatentNumber: "11000001", FilingDate: "2015-01-01"}},
			{ApplicationMetaData: usptoApplicationMetaData{InventionTitle: "InRange", PatentNumber: "11000002", FilingDate: "2018-06-15"}},
			{ApplicationMetaData: usptoApplicationMetaData{InventionTitle: "New", PatentNumber: "11000003", FilingDate: "2023-01-01"}},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		Query:      "x",
		YearFrom:   2016,
		YearTo:     2020,
		NumResults: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result within 2016-2020, got %d: %+v", len(results), results)
	}
	if results[0].Title != "InRange" {
		t.Errorf("expected the in-range result, got %q", results[0].Title)
	}
}

// TestUSPTOProvider_YearRangeFilter_KeepsUnparseableDates proves a result
// with a missing or unparseable Filed date is kept rather than dropped when a
// year range is requested — there's no evidence it's out of range.
func TestUSPTOProvider_YearRangeFilter_KeepsUnparseableDates(t *testing.T) {
	t.Parallel()

	response := usptoResponse{
		Count: 1,
		PatentFileWrapperDataBag: []usptoFileWrapperDataBag{
			{ApplicationMetaData: usptoApplicationMetaData{InventionTitle: "NoDate", PatentNumber: "11000004", FilingDate: ""}},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		Query:      "x",
		YearFrom:   2016,
		YearTo:     2020,
		NumResults: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected the unparseable-date result to be kept, got %d results", len(results))
	}
}

// TestUSPTOProvider_CPCCodeFilter proves the #530 fix: cpc_code is enforced
// client-side by prefix-matching CPCClassificationBag, since USPTO's PEDS API
// `q` full-text search has no CPC query parameter. Without the filter, both
// results would come back regardless of the requested CPC code.
func TestUSPTOProvider_CPCCodeFilter(t *testing.T) {
	t.Parallel()

	response := usptoResponse{
		Count: 2,
		PatentFileWrapperDataBag: []usptoFileWrapperDataBag{
			{ApplicationMetaData: usptoApplicationMetaData{InventionTitle: "Video Codec", PatentNumber: "11000005", CPCClassificationBag: []string{"H04N21/234"}}},
			{ApplicationMetaData: usptoApplicationMetaData{InventionTitle: "Battery Cell", PatentNumber: "11000006", CPCClassificationBag: []string{"H01M10/00"}}},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		Query:      "x",
		CPCCode:    "H04N21",
		NumResults: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result matching CPC prefix, got %d: %+v", len(results), results)
	}
	if results[0].Title != "Video Codec" {
		t.Errorf("expected the matching result, got %q", results[0].Title)
	}
}

func TestMatchesCPCCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		symbols []string
		code    string
		want    bool
	}{
		{name: "exact prefix match", symbols: []string{"G06F17/30"}, code: "G06F", want: true},
		{name: "case insensitive", symbols: []string{"g06f17/30"}, code: "G06F", want: true},
		{name: "no match", symbols: []string{"H01M10/00"}, code: "G06F", want: false},
		{name: "empty symbols", symbols: nil, code: "G06F", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesCPCCode(tt.symbols, tt.code)
			if got != tt.want {
				t.Errorf("matchesCPCCode(%v, %q) = %v, want %v", tt.symbols, tt.code, got, tt.want)
			}
		})
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
			want:   `"video encoding"`,
		},
		{
			name:   "with assignee",
			params: PatentSearchParams{Query: "video", Assignee: "Kaltura"},
			want:   `"video" "Kaltura"`,
		},
		{
			name:   "query with inventor",
			params: PatentSearchParams{Query: "AI", Inventor: "Smith"},
			want:   `"AI" "Smith"`,
		},
		{
			name:   "assignee only",
			params: PatentSearchParams{Assignee: "Google"},
			want:   `"Google"`,
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
