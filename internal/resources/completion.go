package resources

import (
	"context"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxCompletionValues caps a completion response per the MCP spec (a server
// SHOULD return at most 100 values). When more match, Total reflects the full
// count and HasMore is set so the client knows the list was truncated.
const maxCompletionValues = 100

// CompletionSuppliers provides the live, server-known value sets for the
// argument names we can autocomplete. Each is a closure so the handler reflects
// the CURRENT registry/provider state at request time (lenses can be loaded from
// disk; provider maps are built at startup) without the resources package
// importing search or tools. A nil supplier means "no completions for that
// argument" — the handler degrades to an empty result, never an error.
type CompletionSuppliers struct {
	// Lenses returns the names of every registered search lens (bundled +
	// on-disk + custom). Completes any argument named "lens".
	Lenses func() []string
	// Providers returns the names of every configured/known provider across all
	// capability families. Completes any argument named "provider".
	Providers func() []string
}

// staticEnums are argument names whose valid values are a fixed, code-defined
// enum (not registry-driven). Kept here so the handler offers them for free —
// e.g. the comprehensive-research prompt's depth selector.
var staticEnums = map[string][]string{
	"depth": {"quick", "standard", "deep"},
}

// NewCompletionHandler builds the server's completion/complete handler (#193).
// It completes prompt-argument values the server knows the full set of: "lens"
// and "provider" (from the injected suppliers) plus fixed enums like "depth".
// Any other argument — or a nil/empty supplier — yields an empty CompleteResult
// (no completion), never an error, so an unknown argument is silently ignored as
// the spec intends. Matching is case-insensitive prefix on argument.Value.
//
// Setting this on mcp.ServerOptions.CompletionHandler makes the SDK advertise
// the completions capability automatically.
func NewCompletionHandler(s CompletionSuppliers) func(context.Context, *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	return func(_ context.Context, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
		var candidates []string
		switch req.Params.Argument.Name {
		case "lens":
			if s.Lenses != nil {
				candidates = s.Lenses()
			}
		case "provider":
			if s.Providers != nil {
				candidates = s.Providers()
			}
		default:
			candidates = staticEnums[req.Params.Argument.Name]
		}

		values, total, hasMore := filterCompletions(candidates, req.Params.Argument.Value)
		return &mcp.CompleteResult{
			Completion: mcp.CompletionResultDetails{
				Values:  values,
				Total:   total,
				HasMore: hasMore,
			},
		}, nil
	}
}

// filterCompletions returns the candidates matching prefix (case-insensitive),
// sorted and de-duplicated, capped at maxCompletionValues. total is the full
// match count (pre-cap) and hasMore reports whether the cap truncated the list.
// A nil/empty candidate set returns an empty, non-nil slice (valid empty result).
func filterCompletions(candidates []string, prefix string) (values []string, total int, hasMore bool) {
	needle := strings.ToLower(strings.TrimSpace(prefix))
	seen := make(map[string]struct{}, len(candidates))
	matches := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if needle != "" && !strings.HasPrefix(strings.ToLower(c), needle) {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		matches = append(matches, c)
	}
	sort.Strings(matches)
	total = len(matches)
	if total > maxCompletionValues {
		hasMore = true
		matches = matches[:maxCompletionValues]
	}
	return matches, total, hasMore
}
