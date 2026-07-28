package tools

import (
	"context"
	"time"

	"github.com/zendev-sh/goai"
	"github.com/zendev-sh/goai/provider"
)

// ModelProvider is the internal interface research_panel uses to query one
// panel member. Each instance wraps a single configured goai model.
type ModelProvider interface {
	Ask(ctx context.Context, question string) (ModelResponse, error)
	Name() string
	ModelID() string
}

// ModelResponse is one panel member's answer to the research question.
type ModelResponse struct {
	Text         string
	InputTokens  int
	OutputTokens int
	LatencyMs    int64
}

// goaiModelProvider adapts a goai provider.LanguageModel to ModelProvider.
type goaiModelProvider struct {
	name    string
	modelID string
	model   provider.LanguageModel
}

// newGoaiModelProvider wraps a goai language model for the research panel.
func newGoaiModelProvider(name, modelID string, model provider.LanguageModel) *goaiModelProvider {
	return &goaiModelProvider{name: name, modelID: modelID, model: model}
}

func (p *goaiModelProvider) Name() string    { return p.name }
func (p *goaiModelProvider) ModelID() string { return p.modelID }

func (p *goaiModelProvider) Ask(ctx context.Context, question string) (ModelResponse, error) {
	start := time.Now()
	result, err := goai.GenerateText(ctx, p.model, goai.WithPrompt(question))
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return ModelResponse{LatencyMs: latency}, err
	}
	return ModelResponse{
		Text:         result.Text,
		InputTokens:  result.TotalUsage.InputTokens,
		OutputTokens: result.TotalUsage.OutputTokens,
		LatencyMs:    latency,
	}, nil
}
