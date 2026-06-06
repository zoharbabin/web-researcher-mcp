package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newUnpaywallTestResolver(t *testing.T, handler http.HandlerFunc) *UnpaywallResolver {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	r := NewUnpaywallResolver("test@example.com", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	r.SetBaseURL(srv.URL)
	return r
}

func TestUnpaywallResolveOA(t *testing.T) {
	var gotPath string
	r := newUnpaywallTestResolver(t, func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.String()
		_, _ = w.Write([]byte(`{"is_oa":true,"oa_status":"green","best_oa_location":{"url_for_pdf":"https://x/p.pdf","url_for_landing_page":"https://x/p"}}`))
	})
	oa, pdf, found, err := r.Resolve(context.Background(), "10.1/abc")
	if err != nil || !found {
		t.Fatalf("expected found, no error; got found=%v err=%v", found, err)
	}
	if !oa || pdf != "https://x/p.pdf" {
		t.Errorf("oa=%v pdf=%q", oa, pdf)
	}
	if !strings.Contains(gotPath, "email=test%40example.com") {
		t.Errorf("email not sent: %s", gotPath)
	}
}

func TestUnpaywallNormalizesDOIURL(t *testing.T) {
	var gotPath string
	r := newUnpaywallTestResolver(t, func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		_, _ = w.Write([]byte(`{"is_oa":false}`))
	})
	_, _, _, _ = r.Resolve(context.Background(), "https://doi.org/10.1/abc")
	if !strings.HasSuffix(gotPath, "/10.1/abc") {
		t.Errorf("doi.org prefix not stripped: %s", gotPath)
	}
}

func TestUnpaywallFallsBackToLandingURL(t *testing.T) {
	r := newUnpaywallTestResolver(t, func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(`{"is_oa":true,"best_oa_location":{"url_for_pdf":"","url_for_landing_page":"https://x/landing"}}`))
	})
	_, pdf, _, _ := r.Resolve(context.Background(), "10.1/x")
	if pdf != "https://x/landing" {
		t.Errorf("should fall back to landing URL, got %q", pdf)
	}
}

func TestUnpaywall404IsNoOpNotError(t *testing.T) {
	r := newUnpaywallTestResolver(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(404)
	})
	_, _, found, err := r.Resolve(context.Background(), "10.0/missing")
	if err != nil {
		t.Errorf("404 must not be an error, got %v", err)
	}
	if found {
		t.Error("404 should report found=false")
	}
}

func TestUnpaywallEmptyEmailYieldsNilResolver(t *testing.T) {
	if NewUnpaywallResolver("", Deps{}) != nil {
		t.Error("empty email should yield a nil resolver")
	}
	if NewUnpaywallResolver("   ", Deps{}) != nil {
		t.Error("blank email should yield a nil resolver")
	}
}

// fakeResolver drives EnrichOpenAccess deterministically.
type fakeResolver struct {
	oa    bool
	pdf   string
	found bool
	err   error
	calls int
}

func (f *fakeResolver) Name() string { return "fake" }
func (f *fakeResolver) Resolve(_ context.Context, _ string) (bool, string, bool, error) {
	f.calls++
	return f.oa, f.pdf, f.found, f.err
}

func TestEnrichOpenAccess_FillsBareDOI(t *testing.T) {
	f := &fakeResolver{oa: true, pdf: "https://x/p.pdf", found: true}
	in := []AcademicResult{{Title: "P", DOI: "10.1/x"}}
	out := EnrichOpenAccess(context.Background(), f, in)
	if !out[0].OpenAccess || out[0].PDFUrl != "https://x/p.pdf" {
		t.Errorf("not enriched: %+v", out[0])
	}
	if f.calls != 1 {
		t.Errorf("expected 1 resolve call, got %d", f.calls)
	}
}

func TestEnrichOpenAccess_NeverOverwritesProviderPDF(t *testing.T) {
	f := &fakeResolver{oa: true, pdf: "https://unpaywall/p.pdf", found: true}
	in := []AcademicResult{{Title: "P", DOI: "10.1/x", PDFUrl: "https://provider/p.pdf"}}
	out := EnrichOpenAccess(context.Background(), f, in)
	if out[0].PDFUrl != "https://provider/p.pdf" {
		t.Errorf("provider PDF was overwritten: %q", out[0].PDFUrl)
	}
	if f.calls != 0 {
		t.Errorf("should skip resolve when PDF already present, got %d calls", f.calls)
	}
}

func TestEnrichOpenAccess_SkipsNoDOI(t *testing.T) {
	f := &fakeResolver{found: true}
	in := []AcademicResult{{Title: "no doi"}}
	EnrichOpenAccess(context.Background(), f, in)
	if f.calls != 0 {
		t.Errorf("should not resolve a result without a DOI, got %d calls", f.calls)
	}
}

func TestEnrichOpenAccess_GracefulOnError(t *testing.T) {
	f := &fakeResolver{err: context.DeadlineExceeded}
	in := []AcademicResult{{Title: "P", DOI: "10.1/x"}}
	out := EnrichOpenAccess(context.Background(), f, in)
	if out[0].PDFUrl != "" || out[0].OpenAccess {
		t.Error("resolver error should leave result unenriched")
	}
}

func TestEnrichOpenAccess_NilResolverNoOp(t *testing.T) {
	in := []AcademicResult{{Title: "P", DOI: "10.1/x"}}
	out := EnrichOpenAccess(context.Background(), nil, in)
	if len(out) != 1 || out[0].PDFUrl != "" {
		t.Error("nil resolver must be a no-op")
	}
}

func TestUnpaywallInterface(t *testing.T) {
	var _ OAResolver = (*UnpaywallResolver)(nil)
}
