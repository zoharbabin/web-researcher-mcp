package tools

import (
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/config"
)

func TestAvailableModelProviders_NoCredentials(t *testing.T) {
	// bedrockProviderFromEnv reads AWS_* directly via os.Getenv (issue #302: no
	// dedicated config fields, matches other AWS-CLI-configured tools) — clear
	// them so this test is deterministic regardless of the host's shell env.
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_REGION", "")

	providers := AvailableModelProviders(config.ResearchPanelConfig{}, false)
	if len(providers) != 0 {
		t.Errorf("expected zero providers with no credentials configured, got %d", len(providers))
	}
}

func TestAvailableModelProviders_OpenRouterTakesPriority(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_REGION", "")
	cfg := config.ResearchPanelConfig{
		OpenRouterAPIKey: "test-key",
		AnthropicAPIKey:  "test-key", // must be ignored: OpenRouter wins priority
	}
	providers := AvailableModelProviders(cfg, false)
	if len(providers) == 0 {
		t.Fatalf("expected OpenRouter-backed providers, got none")
	}
	for _, p := range providers {
		if p.Name() != "openrouter" {
			t.Errorf("expected all default-panel providers to be openrouter when OPENROUTER_API_KEY is set, got %q", p.Name())
		}
	}
}

func TestAvailableModelProviders_DirectKeysWhenNoOpenRouter(t *testing.T) {
	cfg := config.ResearchPanelConfig{
		AnthropicAPIKey: "test-key",
		OpenAIAPIKey:    "test-key",
	}
	providers := AvailableModelProviders(cfg, false)
	names := map[string]bool{}
	for _, p := range providers {
		names[p.Name()] = true
	}
	if !names["anthropic"] || !names["openai"] {
		t.Errorf("expected both anthropic and openai providers, got %v", names)
	}
}

func TestAvailableModelProviders_MaxModelsClamp(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_REGION", "")
	cfg := config.ResearchPanelConfig{
		OpenRouterAPIKey: "test-key",
		MaxModels:        1,
	}
	providers := AvailableModelProviders(cfg, false)
	if len(providers) != 1 {
		t.Errorf("expected panel clamped to MaxModels=1, got %d", len(providers))
	}
}

func TestAvailableModelProviders_DefaultModelsOverride(t *testing.T) {
	cfg := config.ResearchPanelConfig{
		OpenAIAPIKey:  "test-key",
		DefaultModels: []string{"openai/gpt-4o-mini"},
	}
	providers := AvailableModelProviders(cfg, false)
	if len(providers) != 1 {
		t.Fatalf("expected exactly 1 provider from DefaultModels override, got %d", len(providers))
	}
	if providers[0].Name() != "openai" || providers[0].ModelID() != "gpt-4o-mini" {
		t.Errorf("expected openai/gpt-4o-mini, got %s/%s", providers[0].Name(), providers[0].ModelID())
	}
}

func TestAvailableModelProviders_DefaultModelsUnresolvableSpecDropped(t *testing.T) {
	cfg := config.ResearchPanelConfig{
		// No credentials configured for anthropic — the spec must resolve to
		// nothing rather than panic or silently fabricate a provider.
		DefaultModels: []string{"anthropic/claude-sonnet-4-6"},
	}
	providers := AvailableModelProviders(cfg, false)
	if len(providers) != 0 {
		t.Errorf("expected zero providers when the requested spec's credentials are absent, got %d", len(providers))
	}
}

func TestAvailableModelProviders_OllamaGatedByAllowPrivateIPs(t *testing.T) {
	cfg := config.ResearchPanelConfig{}
	providers := AvailableModelProviders(cfg, false)
	for _, p := range providers {
		if p.Name() == "ollama" {
			t.Errorf("ollama must not be auto-added without OllamaBaseURL or allowPrivateIPs")
		}
	}

	providersAllowed := AvailableModelProviders(cfg, true)
	found := false
	for _, p := range providersAllowed {
		if p.Name() == "ollama" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ollama to be auto-added when allowPrivateIPs=true")
	}
}

func TestResolveModelProviderSpec_MalformedSpec(t *testing.T) {
	cfg := config.ResearchPanelConfig{OpenAIAPIKey: "test-key"}
	if p := resolveModelProviderSpec("openai", cfg, false); p != nil {
		t.Errorf("expected nil for a spec missing the '/' separator, got %v", p)
	}
	if p := resolveModelProviderSpec("openai/", cfg, false); p != nil {
		t.Errorf("expected nil for a spec with an empty model ID, got %v", p)
	}
}

func TestClampModelProviders_ZeroOrNegativeMaxUsesDefault(t *testing.T) {
	providers := []ModelProvider{
		&mockModelProvider{name: "a"}, &mockModelProvider{name: "b"},
		&mockModelProvider{name: "c"}, &mockModelProvider{name: "d"},
	}
	got := clampModelProviders(providers, 0)
	if len(got) != defaultResearchPanelMaxModels {
		t.Errorf("expected clamp to defaultResearchPanelMaxModels=%d, got %d", defaultResearchPanelMaxModels, len(got))
	}
}
