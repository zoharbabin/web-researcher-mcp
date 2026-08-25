package search

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// leakyProvider always fails with an error whose text embeds a credential
// pattern, simulating an upstream that echoes request/response secrets back
// into its error text (e.g. a Lens 401 body, or a Google API error URL).
type leakyProvider struct {
	name string
}

func (p *leakyProvider) Web(_ context.Context, _ WebSearchParams) ([]SearchResult, error) {
	return nil, fmt.Errorf("%s: request failed: key=AIzaSyA1234567890123456789012345678901234", p.name)
}
func (p *leakyProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, fmt.Errorf("not implemented")
}
func (p *leakyProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return nil, fmt.Errorf("not implemented")
}
func (p *leakyProvider) Name() string { return p.name }

// TestRouter_FallbackLogRedactsSecrets guards against a provider error whose
// text embeds a credential (e.g. Lens's 401 body, or a Google key echoed in
// an error URL) reaching the router's fallback-warning log unmasked. Per
// CLAUDE.md Security Rule 3 ("never log secrets, even at debug level"), the
// router must mask err.Error() before handing it to the logger, exactly as
// audit.MaskSecrets already does for the tool-facing error path.
func TestRouter_FallbackLogRedactsSecrets(t *testing.T) {
	leaky := &leakyProvider{name: "leaky"}
	healthy := newChaosProvider("healthy", true)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	providers := map[string]Provider{"leaky": leaky, "healthy": healthy}
	r := NewRouter(providers, RouterConfig{
		Routing: RoutingConfig{Default: []string{"leaky", "healthy"}},
		Logger:  logger,
	})

	if _, err := r.Web(context.Background(), WebSearchParams{Query: "q"}); err != nil {
		t.Fatalf("expected fallback to healthy provider to succeed, got %v", err)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "provider failed") {
		t.Fatalf("expected a fallback log line, got: %q", logged)
	}
	if strings.Contains(logged, "AIzaSyA1234567890123456789012345678901234") {
		t.Errorf("router fallback log leaked a raw API key: %q", logged)
	}
	if !strings.Contains(logged, "[REDACTED]") {
		t.Errorf("expected the logged error to be redacted, got: %q", logged)
	}
}
