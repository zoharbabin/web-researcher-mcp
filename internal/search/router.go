package search

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

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
type Router struct {
	mu        sync.RWMutex
	providers map[string]Provider
	breakers  map[string]*circuit.Breaker
	routing   RoutingConfig
	notifier  FallbackNotifier
	logger    *slog.Logger
}

// RouterConfig configures the Router.
type RouterConfig struct {
	Routing  RoutingConfig
	Notifier FallbackNotifier
	Logger   *slog.Logger
}

// NewRouter creates a multi-provider router. Providers must be pre-constructed
// and keyed by their Name(). Each gets its own circuit breaker for isolation.
func NewRouter(providers map[string]Provider, cfg RouterConfig) *Router {
	breakers := make(map[string]*circuit.Breaker, len(providers))
	for name := range providers {
		breakers[name] = circuit.New(circuit.Config{
			FailureThreshold: 3,
			ResetTimeout:     30,
			HalfOpenAttempts: 1,
		})
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Router{
		providers: providers,
		breakers:  breakers,
		routing:   cfg.Routing,
		notifier:  cfg.Notifier,
		logger:    logger,
	}
}

func (r *Router) Name() string { return "router" }

func (r *Router) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	priority := r.priorityFor(OpWeb)
	var lastErr error
	for i, name := range priority {
		p, ok := r.provider(name)
		if !ok {
			continue
		}
		if !r.isHealthy(name) {
			if i > 0 {
				r.notifyFallback(OpWeb, priority[i-1], name, "circuit_open")
			}
			continue
		}

		results, err := p.Web(ctx, params)
		if err == nil {
			r.recordSuccess(name)
			return results, nil
		}

		lastErr = err
		r.recordFailure(name)
		r.logger.Warn("provider failed, trying next",
			"provider", name, "operation", "web", "error", err)

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
	priority := r.priorityFor(OpImages)
	var lastErr error
	for i, name := range priority {
		p, ok := r.provider(name)
		if !ok {
			continue
		}
		if !r.isHealthy(name) {
			if i > 0 {
				r.notifyFallback(OpImages, priority[i-1], name, "circuit_open")
			}
			continue
		}

		results, err := p.Images(ctx, params)
		if err == nil {
			r.recordSuccess(name)
			return results, nil
		}

		lastErr = err
		r.recordFailure(name)
		r.logger.Warn("provider failed, trying next",
			"provider", name, "operation", "images", "error", err)

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
	priority := r.priorityFor(OpNews)
	var lastErr error
	for i, name := range priority {
		p, ok := r.provider(name)
		if !ok {
			continue
		}
		if !r.isHealthy(name) {
			if i > 0 {
				r.notifyFallback(OpNews, priority[i-1], name, "circuit_open")
			}
			continue
		}

		results, err := p.News(ctx, params)
		if err == nil {
			r.recordSuccess(name)
			return results, nil
		}

		lastErr = err
		r.recordFailure(name)
		r.logger.Warn("provider failed, trying next",
			"provider", name, "operation", "news", "error", err)

		if i+1 < len(priority) {
			r.notifyFallback(OpNews, name, priority[i+1], err.Error())
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no providers available for news search")
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

func (r *Router) isHealthy(name string) bool {
	r.mu.RLock()
	breaker, ok := r.breakers[name]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	return breaker.State() != circuit.StateOpen
}

func (r *Router) recordSuccess(name string) {
	r.mu.RLock()
	breaker, ok := r.breakers[name]
	r.mu.RUnlock()
	if ok {
		_ = breaker.Execute(func() error { return nil })
	}
}

func (r *Router) recordFailure(name string) {
	r.mu.RLock()
	breaker, ok := r.breakers[name]
	r.mu.RUnlock()
	if ok {
		_ = breaker.Execute(func() error { return fmt.Errorf("recorded failure") })
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
