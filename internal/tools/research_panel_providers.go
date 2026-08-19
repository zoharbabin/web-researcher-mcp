package tools

import (
	"os"
	"strings"

	"github.com/zendev-sh/goai/provider/anthropic"
	"github.com/zendev-sh/goai/provider/bedrock"
	"github.com/zendev-sh/goai/provider/compat"
	"github.com/zendev-sh/goai/provider/google"
	"github.com/zendev-sh/goai/provider/ollama"
	"github.com/zendev-sh/goai/provider/openai"
	"github.com/zendev-sh/goai/provider/openrouter"

	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

// Defaults used when auto-detecting a panel without an explicit model list.
const (
	defaultOpenRouterAnthropicModel = "anthropic/claude-sonnet-5"
	defaultOpenRouterOpenAIModel    = "openai/gpt-5.6-sol"
	defaultOpenRouterGoogleModel    = "google/gemini-3.5-flash"
	defaultAnthropicModel           = "claude-sonnet-5"
	defaultOpenAIModel              = "gpt-5.6-sol"
	defaultGoogleModel              = "gemini-3.5-flash"
	defaultBedrockModel             = "anthropic.claude-sonnet-5"
	defaultOllamaModel              = "llama3.2"
	defaultLMStudioModel            = "local-model"
	defaultOllamaBaseURL            = "http://localhost:11434"
	defaultLMStudioBaseURL          = "http://localhost:1234"
	defaultResearchPanelMaxModels   = 3
)

// AvailableModelProviders auto-detects the research_panel's default panel
// from configured credentials, in priority order: OpenRouter (single key,
// widest model coverage) > direct provider keys > AWS Bedrock > local
// Ollama/LM Studio. RESEARCH_PANEL_DEFAULT_MODELS (cfg.DefaultModels)
// overrides auto-detection entirely; short of that, each provider's model
// within auto-detection can be overridden individually via its own
// RESEARCH_PANEL_<PROVIDER>_MODEL env var (see ResearchPanelConfig), falling
// back to the default* constants below. Returns an empty slice when nothing
// is configured — the caller skips registering research_panel in that case.
func AvailableModelProviders(cfg config.ResearchPanelConfig, allowPrivateIPs bool) []ModelProvider {
	var providers []ModelProvider

	if len(cfg.DefaultModels) > 0 {
		for _, spec := range cfg.DefaultModels {
			if p := resolveModelProviderSpec(spec, cfg, allowPrivateIPs); p != nil {
				providers = append(providers, p)
			}
		}
		return clampModelProviders(providers, cfg.MaxModels)
	}

	switch {
	case cfg.OpenRouterAPIKey != "":
		anthropicModel := modelOrDefault(cfg.OpenRouterAnthropicModel, defaultOpenRouterAnthropicModel)
		openaiModel := modelOrDefault(cfg.OpenRouterOpenAIModel, defaultOpenRouterOpenAIModel)
		googleModel := modelOrDefault(cfg.OpenRouterGoogleModel, defaultOpenRouterGoogleModel)
		providers = append(providers,
			newGoaiModelProvider("openrouter", anthropicModel,
				openrouter.Chat(anthropicModel, openrouter.WithAPIKey(cfg.OpenRouterAPIKey))),
			newGoaiModelProvider("openrouter", openaiModel,
				openrouter.Chat(openaiModel, openrouter.WithAPIKey(cfg.OpenRouterAPIKey))),
			newGoaiModelProvider("openrouter", googleModel,
				openrouter.Chat(googleModel, openrouter.WithAPIKey(cfg.OpenRouterAPIKey))),
		)
	default:
		if cfg.AnthropicAPIKey != "" {
			model := modelOrDefault(cfg.AnthropicModel, defaultAnthropicModel)
			providers = append(providers, newGoaiModelProvider("anthropic", model,
				anthropic.Chat(model, anthropic.WithAPIKey(cfg.AnthropicAPIKey))))
		}
		if cfg.OpenAIAPIKey != "" {
			model := modelOrDefault(cfg.OpenAIModel, defaultOpenAIModel)
			providers = append(providers, newGoaiModelProvider("openai", model,
				openai.Chat(model, openai.WithAPIKey(cfg.OpenAIAPIKey))))
		}
		if cfg.GoogleAIAPIKey != "" {
			model := modelOrDefault(cfg.GoogleModel, defaultGoogleModel)
			providers = append(providers, newGoaiModelProvider("google", model,
				google.Chat(model, google.WithAPIKey(cfg.GoogleAIAPIKey))))
		}
	}

	if p := bedrockProviderFromEnv(modelOrDefault(cfg.BedrockModel, defaultBedrockModel)); p != nil {
		providers = append(providers, p)
	}

	if cfg.OllamaBaseURL != "" || allowPrivateIPs {
		providers = append(providers, ollamaProvider(cfg.OllamaBaseURL, modelOrDefault(cfg.OllamaModel, defaultOllamaModel), allowPrivateIPs))
	}
	if cfg.LMStudioBaseURL != "" || allowPrivateIPs {
		providers = append(providers, lmStudioProvider(cfg.LMStudioBaseURL, modelOrDefault(cfg.LMStudioModel, defaultLMStudioModel), allowPrivateIPs))
	}

	maxModels := cfg.MaxModels
	if maxModels <= 0 {
		maxModels = defaultResearchPanelMaxModels
	}
	return clampModelProviders(providers, maxModels)
}

// bedrockProviderFromEnv adds an AWS Bedrock Claude model when standard AWS
// credentials are present. Bedrock has no dedicated credential config fields
// — it reads AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY/AWS_REGION itself,
// matching every other AWS CLI-configured tool (no new env vars per issue
// #302); its default model is overridable via RESEARCH_PANEL_BEDROCK_MODEL.
func bedrockProviderFromEnv(model string) ModelProvider {
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" || os.Getenv("AWS_REGION") == "" {
		return nil
	}
	return newGoaiModelProvider("bedrock", model, bedrock.Chat(model))
}

func ollamaProvider(baseURL, model string, allowPrivateIPs bool) ModelProvider {
	if baseURL == "" {
		baseURL = defaultOllamaBaseURL
	}
	client := scraper.NewSSRFSafeClient(allowPrivateIPs)
	chat := ollama.Chat(model, ollama.WithBaseURL(baseURL), ollama.WithHTTPClient(client))
	return newGoaiModelProvider("ollama", model, chat)
}

func lmStudioProvider(baseURL, model string, allowPrivateIPs bool) ModelProvider {
	if baseURL == "" {
		baseURL = defaultLMStudioBaseURL
	}
	client := scraper.NewSSRFSafeClient(allowPrivateIPs)
	chat := compat.Chat(model,
		compat.WithProviderID("lmstudio"),
		compat.WithBaseURL(strings.TrimSuffix(baseURL, "/")+"/v1"),
		compat.WithHTTPClient(client),
	)
	return newGoaiModelProvider("lmstudio", model, chat)
}

// modelOrDefault returns override when non-empty, else fallback — the shared
// pattern behind every RESEARCH_PANEL_<PROVIDER>_MODEL env var override.
func modelOrDefault(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

// resolveModelProviderSpec parses a "<provider>/<model-id>" spec (the
// provider is the segment before the first '/'; the model ID is everything
// after, since OpenRouter/Bedrock model IDs themselves contain '/' or '.').
// Returns nil when the named provider's credentials aren't configured.
func resolveModelProviderSpec(spec string, cfg config.ResearchPanelConfig, allowPrivateIPs bool) ModelProvider {
	providerName, modelID, ok := strings.Cut(spec, "/")
	if !ok || modelID == "" {
		return nil
	}
	switch providerName {
	case "openrouter":
		if cfg.OpenRouterAPIKey == "" {
			return nil
		}
		return newGoaiModelProvider("openrouter", modelID, openrouter.Chat(modelID, openrouter.WithAPIKey(cfg.OpenRouterAPIKey)))
	case "anthropic":
		if cfg.AnthropicAPIKey == "" {
			return nil
		}
		return newGoaiModelProvider("anthropic", modelID, anthropic.Chat(modelID, anthropic.WithAPIKey(cfg.AnthropicAPIKey)))
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			return nil
		}
		return newGoaiModelProvider("openai", modelID, openai.Chat(modelID, openai.WithAPIKey(cfg.OpenAIAPIKey)))
	case "google":
		if cfg.GoogleAIAPIKey == "" {
			return nil
		}
		return newGoaiModelProvider("google", modelID, google.Chat(modelID, google.WithAPIKey(cfg.GoogleAIAPIKey)))
	case "bedrock":
		if os.Getenv("AWS_ACCESS_KEY_ID") == "" || os.Getenv("AWS_REGION") == "" {
			return nil
		}
		return newGoaiModelProvider("bedrock", modelID, bedrock.Chat(modelID))
	case "ollama":
		if cfg.OllamaBaseURL == "" && !allowPrivateIPs {
			return nil
		}
		baseURL := cfg.OllamaBaseURL
		if baseURL == "" {
			baseURL = defaultOllamaBaseURL
		}
		client := scraper.NewSSRFSafeClient(allowPrivateIPs)
		return newGoaiModelProvider("ollama", modelID, ollama.Chat(modelID, ollama.WithBaseURL(baseURL), ollama.WithHTTPClient(client)))
	case "lmstudio":
		if cfg.LMStudioBaseURL == "" && !allowPrivateIPs {
			return nil
		}
		baseURL := cfg.LMStudioBaseURL
		if baseURL == "" {
			baseURL = defaultLMStudioBaseURL
		}
		client := scraper.NewSSRFSafeClient(allowPrivateIPs)
		model := compat.Chat(modelID,
			compat.WithProviderID("lmstudio"),
			compat.WithBaseURL(strings.TrimSuffix(baseURL, "/")+"/v1"),
			compat.WithHTTPClient(client),
		)
		return newGoaiModelProvider("lmstudio", modelID, model)
	default:
		return nil
	}
}

func clampModelProviders(providers []ModelProvider, max int) []ModelProvider {
	if max <= 0 {
		max = defaultResearchPanelMaxModels
	}
	if len(providers) > max {
		return providers[:max]
	}
	return providers
}
