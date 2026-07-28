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
	defaultOpenRouterAnthropicModel = "anthropic/claude-sonnet-4-6"
	defaultOpenRouterOpenAIModel    = "openai/gpt-4o"
	defaultOpenRouterGoogleModel    = "google/gemini-2.5-pro"
	defaultAnthropicModel           = "claude-sonnet-4-6"
	defaultOpenAIModel              = "gpt-4o"
	defaultGoogleModel              = "gemini-2.5-pro"
	defaultBedrockModel             = "anthropic.claude-sonnet-4-20250514-v1:0"
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
// overrides auto-detection entirely. Returns an empty slice when nothing is
// configured — the caller skips registering research_panel in that case.
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
		providers = append(providers,
			newGoaiModelProvider("openrouter", defaultOpenRouterAnthropicModel,
				openrouter.Chat(defaultOpenRouterAnthropicModel, openrouter.WithAPIKey(cfg.OpenRouterAPIKey))),
			newGoaiModelProvider("openrouter", defaultOpenRouterOpenAIModel,
				openrouter.Chat(defaultOpenRouterOpenAIModel, openrouter.WithAPIKey(cfg.OpenRouterAPIKey))),
			newGoaiModelProvider("openrouter", defaultOpenRouterGoogleModel,
				openrouter.Chat(defaultOpenRouterGoogleModel, openrouter.WithAPIKey(cfg.OpenRouterAPIKey))),
		)
	default:
		if cfg.AnthropicAPIKey != "" {
			providers = append(providers, newGoaiModelProvider("anthropic", defaultAnthropicModel,
				anthropic.Chat(defaultAnthropicModel, anthropic.WithAPIKey(cfg.AnthropicAPIKey))))
		}
		if cfg.OpenAIAPIKey != "" {
			providers = append(providers, newGoaiModelProvider("openai", defaultOpenAIModel,
				openai.Chat(defaultOpenAIModel, openai.WithAPIKey(cfg.OpenAIAPIKey))))
		}
		if cfg.GoogleAIAPIKey != "" {
			providers = append(providers, newGoaiModelProvider("google", defaultGoogleModel,
				google.Chat(defaultGoogleModel, google.WithAPIKey(cfg.GoogleAIAPIKey))))
		}
	}

	if p := bedrockProviderFromEnv(); p != nil {
		providers = append(providers, p)
	}

	if cfg.OllamaBaseURL != "" || allowPrivateIPs {
		providers = append(providers, ollamaProvider(cfg.OllamaBaseURL, allowPrivateIPs))
	}
	if cfg.LMStudioBaseURL != "" || allowPrivateIPs {
		providers = append(providers, lmStudioProvider(cfg.LMStudioBaseURL, allowPrivateIPs))
	}

	maxModels := cfg.MaxModels
	if maxModels <= 0 {
		maxModels = defaultResearchPanelMaxModels
	}
	return clampModelProviders(providers, maxModels)
}

// bedrockProviderFromEnv adds an AWS Bedrock Claude model when standard AWS
// credentials are present. Bedrock has no dedicated config fields — it reads
// AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY/AWS_REGION itself, matching every
// other AWS CLI-configured tool (no new env vars per issue #302).
func bedrockProviderFromEnv() ModelProvider {
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" || os.Getenv("AWS_REGION") == "" {
		return nil
	}
	return newGoaiModelProvider("bedrock", defaultBedrockModel, bedrock.Chat(defaultBedrockModel))
}

func ollamaProvider(baseURL string, allowPrivateIPs bool) ModelProvider {
	if baseURL == "" {
		baseURL = defaultOllamaBaseURL
	}
	client := scraper.NewSSRFSafeClient(allowPrivateIPs)
	model := ollama.Chat(defaultOllamaModel, ollama.WithBaseURL(baseURL), ollama.WithHTTPClient(client))
	return newGoaiModelProvider("ollama", defaultOllamaModel, model)
}

func lmStudioProvider(baseURL string, allowPrivateIPs bool) ModelProvider {
	if baseURL == "" {
		baseURL = defaultLMStudioBaseURL
	}
	client := scraper.NewSSRFSafeClient(allowPrivateIPs)
	model := compat.Chat(defaultLMStudioModel,
		compat.WithProviderID("lmstudio"),
		compat.WithBaseURL(strings.TrimSuffix(baseURL, "/")+"/v1"),
		compat.WithHTTPClient(client),
	)
	return newGoaiModelProvider("lmstudio", defaultLMStudioModel, model)
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
