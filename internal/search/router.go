package search

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// webQueryStopWords are common English function words stripped by
// simplifyQuery when a provider returns a thin result set for a naturally
// phrased query (e.g. "what is the capital of france").
var webQueryStopWords = map[string]bool{
	"a": true, "about": true, "an": true, "and": true, "any": true,
	"are": true, "as": true, "at": true, "be": true, "by": true,
	"can": true, "could": true, "did": true, "do": true, "does": true,
	"for": true, "from": true, "had": true, "has": true, "have": true,
	"how": true, "i": true, "in": true, "is": true, "it": true,
	"me": true, "my": true, "of": true, "on": true, "or": true,
	"please": true, "should": true, "so": true, "that": true, "the": true,
	"their": true, "there": true, "this": true, "to": true, "was": true,
	"we": true, "what": true, "when": true, "where": true, "which": true,
	"who": true, "why": true, "will": true, "with": true, "would": true,
	"you": true, "your": true,
}

// simplifyQuery strips stop-words from query, returning the original string
// unchanged when doing so would remove every token or would remove nothing
// (nothing to simplify). Callers should compare the result for equality with
// the input to decide whether a retry is warranted.
func simplifyQuery(query string) string {
	tokens := strings.Fields(query)
	kept := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if !webQueryStopWords[strings.ToLower(tok)] {
			kept = append(kept, tok)
		}
	}
	if len(kept) == 0 || len(kept) == len(tokens) {
		return query
	}
	return strings.Join(kept, " ")
}

// Operation represents a search operation type for routing decisions.
type Operation string

const (
	OpWeb      Operation = "web"
	OpImages   Operation = "images"
	OpNews     Operation = "news"
	OpAcademic Operation = "academic"
	OpPatents  Operation = "patents"
	OpDefault  Operation = "default"
)

// RoutingConfig defines per-operation provider priority lists.
type RoutingConfig struct {
	Web      []string `json:"web,omitempty"`
	Images   []string `json:"images,omitempty"`
	News     []string `json:"news,omitempty"`
	Academic []string `json:"academic,omitempty"`
	Patents  []string `json:"patents,omitempty"`
	Default  []string `json:"default,omitempty"`
}

// FallbackNotifier is called when a fallback occurs. Implementations can send
// MCP notifications, log, or record metrics.
type FallbackNotifier func(op Operation, from, to, reason string)

// Router implements the Provider interface with multi-provider fallback.
// It holds multiple configured providers and routes requests based on
// operation type, provider health (circuit breakers), and priority ordering.
//
// Capability coverage: the Router routes Web/Images/News (Provider),
// Patents (PatentSearcher), and Scholarly (AcademicSearcher) with per-provider
// breaker fallback. Structured-domain capabilities (filing/case/econ/trial/
// monarch/awesome-list/local/context) are resolved directly from the
// Dependencies maps in the tool layer, since each has at most one active
// provider today and needs no fallback ladder.
type Router struct {
	mu                  sync.RWMutex
	providers           map[string]Provider
	breakers            map[string]*circuit.Breaker
	patentProviders     map[string]PatentProvider
	patentBreakers      map[string]*circuit.Breaker
	academicProviders   map[string]AcademicProvider
	academicBreakers    map[string]*circuit.Breaker
	routing             RoutingConfig
	notifier            FallbackNotifier
	logger              *slog.Logger
	thinThreshold       int
	maxFetchPerProvider map[string]int
}

// routerBreakerConfig is a deliberate override of the global circuit-breaker
// default (#468: CIRCUIT_FAILURE_THRESHOLD / CIRCUIT_RESET_TIMEOUT_SECONDS).
// The Router's breakers gate live per-request fallback ordering ("try the
// next provider now"), not long-horizon provider-health tracking — they need
// to trip and reset much faster than the 5-failures/60s global default so a
// degraded provider drops out of rotation within seconds, not a minute.
var routerBreakerConfig = circuit.Config{
	FailureThreshold: 3,
	ResetTimeout:     30,
	HalfOpenAttempts: 1,
}

// Compile-time proof the Router satisfies every capability it routes. These
// also document, at the type, exactly which capabilities the Router covers.
var (
	_ Provider         = (*Router)(nil)
	_ PatentSearcher   = (*Router)(nil)
	_ AcademicSearcher = (*Router)(nil)
)

// RouterConfig configures the Router.
type RouterConfig struct {
	Routing           RoutingConfig
	Notifier          FallbackNotifier
	Logger            *slog.Logger
	PatentProviders   map[string]PatentProvider
	AcademicProviders map[string]AcademicProvider
	// ThinThreshold retries the same provider with a stop-word-stripped query
	// when Web() returns a result count <= this threshold; 0 disables the retry.
	ThinThreshold int
	// MaxFetchPerProvider caps NumResults for the named provider before forwarding.
	// A zero or absent entry means no additional cap (provider's native cap applies).
	MaxFetchPerProvider map[string]int
}

// depthNumResults maps a Depth hint to a default NumResults when the caller
// passes NumResults<=0. Keys are the three canonical depth tiers.
var depthNumResults = map[string]int{
	"quick": 6,
	"":      12,
	"deep":  20,
}

// NewRouter creates a multi-provider router. Providers must be pre-constructed
// and keyed by their Name(). Each gets its own circuit breaker for isolation.
func NewRouter(providers map[string]Provider, cfg RouterConfig) *Router {
	breakers := make(map[string]*circuit.Breaker, len(providers))
	for name := range providers {
		breakers[name] = circuit.New(routerBreakerConfig)
	}

	patentProviders := cfg.PatentProviders
	if patentProviders == nil {
		patentProviders = make(map[string]PatentProvider)
	}
	patentBreakers := make(map[string]*circuit.Breaker, len(patentProviders))
	for name := range patentProviders {
		patentBreakers[name] = circuit.New(routerBreakerConfig)
	}

	academicProviders := cfg.AcademicProviders
	if academicProviders == nil {
		academicProviders = make(map[string]AcademicProvider)
	}
	academicBreakers := make(map[string]*circuit.Breaker, len(academicProviders))
	for name := range academicProviders {
		academicBreakers[name] = circuit.New(routerBreakerConfig)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Router{
		providers:           providers,
		breakers:            breakers,
		patentProviders:     patentProviders,
		patentBreakers:      patentBreakers,
		academicProviders:   academicProviders,
		academicBreakers:    academicBreakers,
		routing:             cfg.Routing,
		notifier:            cfg.Notifier,
		logger:              logger,
		thinThreshold:       cfg.ThinThreshold,
		maxFetchPerProvider: cfg.MaxFetchPerProvider,
	}
}

func (r *Router) Name() string { return "router" }

func (r *Router) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	trace := routingTraceFromContext(ctx)
	priority := r.priorityFor(OpWeb)

	// Apply depth-tiered default when caller did not specify NumResults.
	if params.NumResults <= 0 {
		if n, ok := depthNumResults[params.Depth]; ok {
			params.NumResults = n
		} else {
			params.NumResults = depthNumResults[""] // unknown depth → standard default
		}
	}

	var lastErr error
	for i, name := range priority {
		p, ok := r.provider(name)
		if !ok {
			continue
		}
		trace.attempt(name)
		if !r.isHealthy(name) {
			if i > 0 {
				r.notifyFallback(OpWeb, priority[i-1], name, "circuit_open")
			}
			trace.fellBack(FallbackReasonCircuitOpen)
			continue
		}

		// Apply per-provider NumResults cap; copy params to avoid mutating the shared struct.
		providerParams := params
		if fetchCap, ok := r.maxFetchPerProvider[name]; ok && fetchCap > 0 && providerParams.NumResults > fetchCap {
			providerParams.NumResults = fetchCap
		}

		results, err := p.Web(ctx, providerParams)
		if err == nil {
			if r.thinThreshold > 0 && len(results) <= r.thinThreshold {
				if simplified := simplifyQuery(providerParams.Query); simplified != providerParams.Query {
					retryParams := providerParams
					retryParams.Query = simplified
					if retryResults, retryErr := p.Web(ctx, retryParams); retryErr == nil && len(retryResults) > len(results) {
						results = retryResults
					}
				}
			}
			r.recordSuccess(name)
			trace.served(name)
			return results, nil
		}

		lastErr = err
		r.recordFailure(name, err)
		r.logger.Warn("provider failed, trying next",
			"provider", name, "operation", "web", "error", audit.MaskSecrets(err.Error()))
		trace.fellBack(FallbackReasonPrimaryUnavailable)

		if i+1 < len(priority) {
			r.notifyFallback(OpWeb, name, priority[i+1], err.Error())
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no providers available for web search")
}

func (r *Router) Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	trace := routingTraceFromContext(ctx)
	priority := r.priorityFor(OpImages)
	var lastErr error
	for i, name := range priority {
		p, ok := r.provider(name)
		if !ok {
			continue
		}
		trace.attempt(name)
		if !r.isHealthy(name) {
			if i > 0 {
				r.notifyFallback(OpImages, priority[i-1], name, "circuit_open")
			}
			trace.fellBack(FallbackReasonCircuitOpen)
			continue
		}

		results, err := p.Images(ctx, params)
		if err == nil {
			r.recordSuccess(name)
			trace.served(name)
			return results, nil
		}

		lastErr = err
		r.recordFailure(name, err)
		r.logger.Warn("provider failed, trying next",
			"provider", name, "operation", "images", "error", audit.MaskSecrets(err.Error()))
		trace.fellBack(FallbackReasonPrimaryUnavailable)

		if i+1 < len(priority) {
			r.notifyFallback(OpImages, name, priority[i+1], err.Error())
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no providers available for image search")
}

func (r *Router) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	trace := routingTraceFromContext(ctx)
	priority := r.priorityFor(OpNews)
	var lastErr error
	for i, name := range priority {
		p, ok := r.provider(name)
		if !ok {
			continue
		}
		trace.attempt(name)
		if !r.isHealthy(name) {
			if i > 0 {
				r.notifyFallback(OpNews, priority[i-1], name, "circuit_open")
			}
			trace.fellBack(FallbackReasonCircuitOpen)
			continue
		}

		results, err := p.News(ctx, params)
		if err == nil {
			r.recordSuccess(name)
			trace.served(name)
			return results, nil
		}

		lastErr = err
		r.recordFailure(name, err)
		r.logger.Warn("provider failed, trying next",
			"provider", name, "operation", "news", "error", audit.MaskSecrets(err.Error()))
		trace.fellBack(FallbackReasonPrimaryUnavailable)

		if i+1 < len(priority) {
			r.notifyFallback(OpNews, name, priority[i+1], err.Error())
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no providers available for news search")
}

// Patents implements PatentSearcher by routing to patent-capable providers
// in priority order, with region-aware filtering and circuit breaker fallback.
func (r *Router) Patents(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	trace := routingTraceFromContext(ctx)
	priority := r.patentPriority()
	var lastErr error

	for i, name := range priority {
		// Check full providers that also implement PatentSearcher (e.g. SearchAPI)
		if p, ok := r.provider(name); ok {
			ps, implements := p.(PatentSearcher)
			if !implements {
				continue
			}
			trace.attempt(name)
			if !r.isHealthy(name) {
				trace.fellBack(FallbackReasonCircuitOpen)
				continue
			}
			// Check region metadata if available
			if pp, hasMetadata := p.(PatentProvider); hasMetadata {
				if !pp.Metadata().MatchesRegion(params.PatentOffice) {
					continue
				}
			}
			results, err := ps.Patents(ctx, params)
			if err == nil {
				r.recordSuccess(name)
				trace.served(name)
				return results, nil
			}
			lastErr = err
			r.recordFailure(name, err)
			r.logger.Warn("patent provider failed, trying next",
				"provider", name, "operation", "patents", "error", audit.MaskSecrets(err.Error()))
			trace.fellBack(FallbackReasonPrimaryUnavailable)
			if i+1 < len(priority) {
				r.notifyFallback(OpPatents, name, priority[i+1], err.Error())
			}
			continue
		}

		// Check patent-only providers
		r.mu.RLock()
		pp, ok := r.patentProviders[name]
		breaker, hasBrk := r.patentBreakers[name]
		r.mu.RUnlock()
		if !ok {
			continue
		}
		trace.attempt(name)
		// Ready(), not State()==Open: a passive State() read never performs
		// the Open->HalfOpen transition (#469).
		if hasBrk && !breaker.Ready() {
			trace.fellBack(FallbackReasonCircuitOpen)
			continue
		}
		if !pp.Metadata().MatchesRegion(params.PatentOffice) {
			continue
		}

		results, err := pp.Patents(ctx, params)
		if err == nil {
			if hasBrk {
				_ = breaker.Execute(func() error { return nil })
			}
			trace.served(name)
			return results, nil
		}
		lastErr = err
		if hasBrk {
			_ = breaker.Execute(func() error { return err })
		}
		r.logger.Warn("patent provider failed, trying next",
			"provider", name, "operation", "patents", "error", audit.MaskSecrets(err.Error()))
		trace.fellBack(FallbackReasonPrimaryUnavailable)
		if i+1 < len(priority) {
			r.notifyFallback(OpPatents, name, priority[i+1], err.Error())
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no providers available for patent search")
}

// RegisterPatentProviders adds patent-only providers to the router.
func (r *Router) RegisterPatentProviders(providers map[string]PatentProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, p := range providers {
		r.patentProviders[name] = p
		if _, exists := r.patentBreakers[name]; !exists {
			r.patentBreakers[name] = circuit.New(routerBreakerConfig)
		}
	}
}

// PatentProviderByName returns a patent-capable provider by name.
// Checks both full providers (that implement PatentSearcher) and patent-only providers.
func (r *Router) PatentProviderByName(name string) (PatentSearcher, bool) {
	if p, ok := r.provider(name); ok {
		if ps, implements := p.(PatentSearcher); implements {
			return ps, true
		}
	}
	r.mu.RLock()
	pp, ok := r.patentProviders[name]
	r.mu.RUnlock()
	if ok {
		return pp, true
	}
	return nil, false
}

// patentPriority returns the ordered list of patent provider names to try.
func (r *Router) patentPriority() []string {
	priority := r.priorityFor(OpPatents)

	// Also include patent-only providers not already in the list
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]bool, len(priority))
	for _, name := range priority {
		seen[name] = true
	}
	for name := range r.patentProviders {
		if !seen[name] {
			priority = append(priority, name)
		}
	}
	return priority
}

// Scholarly implements AcademicSearcher by routing to academic-capable providers
// in priority order with circuit breaker fallback.
func (r *Router) Scholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	trace := routingTraceFromContext(ctx)
	priority := r.academicPriority()
	var lastErr error

	for i, name := range priority {
		r.mu.RLock()
		ap, ok := r.academicProviders[name]
		breaker, hasBrk := r.academicBreakers[name]
		r.mu.RUnlock()
		if !ok {
			continue
		}
		trace.attempt(name)
		// Ready(), not State()==Open: a passive State() read never performs
		// the Open->HalfOpen transition (#469).
		if hasBrk && !breaker.Ready() {
			trace.fellBack(FallbackReasonCircuitOpen)
			continue
		}

		results, err := ap.Scholarly(ctx, params)
		if err == nil {
			if hasBrk {
				_ = breaker.Execute(func() error { return nil })
			}
			trace.served(name)
			return results, nil
		}
		lastErr = err
		if hasBrk {
			_ = breaker.Execute(func() error { return err })
		}
		r.logger.Warn("academic provider failed, trying next",
			"provider", name, "operation", "academic", "error", audit.MaskSecrets(err.Error()))
		trace.fellBack(FallbackReasonPrimaryUnavailable)
		if i+1 < len(priority) {
			r.notifyFallback(OpAcademic, name, priority[i+1], err.Error())
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no providers available for academic search")
}

// RegisterAcademicProviders adds academic providers to the router.
func (r *Router) RegisterAcademicProviders(providers map[string]AcademicProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, p := range providers {
		r.academicProviders[name] = p
		if _, exists := r.academicBreakers[name]; !exists {
			r.academicBreakers[name] = circuit.New(routerBreakerConfig)
		}
	}
}

// AcademicProviderByName returns an academic provider by name.
func (r *Router) AcademicProviderByName(name string) (AcademicSearcher, bool) {
	r.mu.RLock()
	ap, ok := r.academicProviders[name]
	r.mu.RUnlock()
	if ok {
		return ap, true
	}
	return nil, false
}

// academicPriority returns the ordered list of academic provider names to try.
func (r *Router) academicPriority() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Use explicit routing config if present
	var configured []string
	if len(r.routing.Academic) > 0 {
		configured = r.routing.Academic
	}

	// Filter to only include providers that are actually registered
	var priority []string
	if len(configured) > 0 {
		for _, name := range configured {
			if _, ok := r.academicProviders[name]; ok {
				priority = append(priority, name)
			}
		}
	}

	// Add any registered providers not already in the priority list, excluding
	// explicit-only providers (e.g. scholarapi) — those are paid/metered and must
	// only be reached via an explicit provider= request or explicit routing config,
	// never auto-fallback.
	seen := make(map[string]bool, len(priority))
	for _, name := range priority {
		seen[name] = true
	}
	explicitOnly := make(map[string]bool, len(AcademicProvidersExplicitOnly))
	for _, name := range AcademicProvidersExplicitOnly {
		explicitOnly[name] = true
	}
	for name := range r.academicProviders {
		if !seen[name] && !explicitOnly[name] {
			priority = append(priority, name)
		}
	}
	return priority
}

// ProviderFor returns the best available provider for a given operation,
// respecting routing config and circuit breaker state. Useful for tools that
// need direct access (academic_search, patent_search).
func (r *Router) ProviderFor(op Operation) (Provider, string) {
	priority := r.priorityFor(op)
	for _, name := range priority {
		p, ok := r.provider(name)
		if !ok {
			continue
		}
		if r.isHealthy(name) {
			return p, name
		}
	}
	return nil, ""
}

// ProviderByName returns a specific provider by name.
// Returns (nil, false) if the provider is not registered.
func (r *Router) ProviderByName(name string) (Provider, bool) {
	return r.provider(name)
}

// Providers returns all registered provider names.
func (r *Router) Providers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

func (r *Router) provider(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// IsHealthy reports whether a registered provider's circuit breaker is not open
// (i.e. it is currently usable). Unknown providers report false. Exported so the
// tools layer can filter zero-result provider suggestions to healthy providers
// only (issue #100), without reaching into breaker internals.
func (r *Router) IsHealthy(name string) bool {
	return r.isHealthy(name)
}

func (r *Router) isHealthy(name string) bool {
	r.mu.RLock()
	breaker, ok := r.breakers[name]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	// Ready(), not State(): a passive State() read never performs the
	// Open->HalfOpen transition, so a tripped breaker would stay Open forever
	// once the Router stops calling Execute on it (#469).
	return breaker.Ready()
}

func (r *Router) recordSuccess(name string) {
	r.mu.RLock()
	breaker, ok := r.breakers[name]
	r.mu.RUnlock()
	if ok {
		_ = breaker.Execute(func() error { return nil })
	}
}

// recordFailure forwards the real provider error to the circuit breaker for
// that provider. If err wraps circuit.ErrRateLimit, the breaker opens
// immediately.
func (r *Router) recordFailure(name string, err error) {
	r.mu.RLock()
	breaker, ok := r.breakers[name]
	r.mu.RUnlock()
	if ok {
		_ = breaker.Execute(func() error { return err })
	}
}

func (r *Router) notifyFallback(op Operation, from, to, reason string) {
	if r.notifier != nil {
		r.notifier(op, from, to, reason)
	}
}

func (r *Router) priorityFor(op Operation) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var priority []string
	switch op {
	case OpWeb:
		priority = r.routing.Web
	case OpImages:
		priority = r.routing.Images
	case OpNews:
		priority = r.routing.News
	case OpAcademic:
		priority = r.routing.Academic
	case OpPatents:
		priority = r.routing.Patents
	}

	if len(priority) > 0 {
		return r.filterAvailable(priority)
	}
	if len(r.routing.Default) > 0 {
		return r.filterAvailable(r.routing.Default)
	}
	return r.filterAvailable(r.allProviderNames())
}

func (r *Router) filterAvailable(names []string) []string {
	available := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := r.providers[name]; ok {
			available = append(available, name)
		}
	}
	return available
}

func (r *Router) allProviderNames() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// ParseRoutingConfig parses the SEARCH_ROUTING env var value.
// Supports two formats:
//   - Simple: "brave,google,serper" (comma-separated, applies to all operations)
//   - JSON: {"web":"brave,google","news":"brave,serper","default":"brave,google"}
func ParseRoutingConfig(value string) (RoutingConfig, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return RoutingConfig{}, nil
	}

	if strings.HasPrefix(value, "{") {
		var raw map[string]string
		if err := json.Unmarshal([]byte(value), &raw); err != nil {
			return RoutingConfig{}, fmt.Errorf("invalid SEARCH_ROUTING JSON: %w", err)
		}
		cfg := RoutingConfig{}
		for key, val := range raw {
			providers := splitProviders(val)
			switch key {
			case "web":
				cfg.Web = providers
			case "images":
				cfg.Images = providers
			case "news":
				cfg.News = providers
			case "academic":
				cfg.Academic = providers
			case "patents":
				cfg.Patents = providers
			case "default":
				cfg.Default = providers
			}
		}
		return cfg, nil
	}

	providers := splitProviders(value)
	return RoutingConfig{Default: providers}, nil
}

func splitProviders(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
