package resources

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newCompletionTestServer wires a server with the #193 completion handler over
// fixed lens/provider sets, then connects an in-memory client. The handler is
// set on ServerOptions so the SDK advertises the completions capability.
func newCompletionTestServer(t *testing.T) *mcp.ClientSession {
	t.Helper()
	suppliers := CompletionSuppliers{
		Lenses:    func() []string { return []string{"arxiv", "pubmed", "gov", "academic"} },
		Providers: func() []string { return []string{"google", "brave", "exa", "duckduckgo"} },
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, &mcp.ServerOptions{
		CompletionHandler: NewCompletionHandler(suppliers),
	})
	// A prompt is needed so completion has a valid ref/prompt to target.
	srv.AddPrompt(&mcp.Prompt{
		Name:      "comprehensive-research",
		Arguments: []*mcp.PromptArgument{{Name: "lens"}, {Name: "depth"}},
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "x"}}}}, nil
	})
	return connectTestClient(context.Background(), t, srv)
}

func complete(t *testing.T, cs *mcp.ClientSession, argName, value string) *mcp.CompletionResultDetails {
	t.Helper()
	res, err := cs.Complete(context.Background(), &mcp.CompleteParams{
		Ref:      &mcp.CompleteReference{Type: "ref/prompt", Name: "comprehensive-research"},
		Argument: mcp.CompleteParamsArgument{Name: argName, Value: value},
	})
	if err != nil {
		t.Fatalf("Complete(%q,%q) failed: %v", argName, value, err)
	}
	return &res.Completion
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestCompletion_Lens(t *testing.T) {
	cs := newCompletionTestServer(t)
	defer cs.Close()

	// No prefix → all lenses, sorted.
	all := complete(t, cs, "lens", "")
	if all.Total != 4 || len(all.Values) != 4 {
		t.Fatalf("lens all: total=%d values=%v, want 4", all.Total, all.Values)
	}
	if all.Values[0] != "academic" {
		t.Errorf("lens values not sorted: %v", all.Values)
	}

	// Prefix filter, case-insensitive.
	pre := complete(t, cs, "lens", "A")
	if !contains(pre.Values, "academic") || !contains(pre.Values, "arxiv") {
		t.Errorf("lens prefix 'A' = %v, want academic+arxiv", pre.Values)
	}
	if contains(pre.Values, "pubmed") {
		t.Errorf("lens prefix 'A' should not include pubmed: %v", pre.Values)
	}
}

func TestCompletion_Provider(t *testing.T) {
	cs := newCompletionTestServer(t)
	defer cs.Close()

	res := complete(t, cs, "provider", "")
	if res.Total != 4 {
		t.Fatalf("provider total=%d, want 4", res.Total)
	}
	if !contains(res.Values, "exa") {
		t.Errorf("provider values = %v, want to contain exa", res.Values)
	}
}

func TestCompletion_DepthEnum(t *testing.T) {
	cs := newCompletionTestServer(t)
	defer cs.Close()

	// depth is a static enum, completed without any supplier.
	res := complete(t, cs, "depth", "")
	if res.Total != 3 {
		t.Fatalf("depth total=%d, want 3 (quick/standard/deep)", res.Total)
	}
	d := complete(t, cs, "depth", "de")
	if len(d.Values) != 1 || d.Values[0] != "deep" {
		t.Errorf("depth prefix 'de' = %v, want [deep]", d.Values)
	}
}

func TestCompletion_UnknownArgument(t *testing.T) {
	cs := newCompletionTestServer(t)
	defer cs.Close()

	// An argument we don't complete returns an empty (non-nil) result, not an error.
	res := complete(t, cs, "topic", "anything")
	if len(res.Values) != 0 || res.Total != 0 {
		t.Fatalf("unknown arg: values=%v total=%d, want empty", res.Values, res.Total)
	}
}

// TestFilterCompletions_Cap verifies the 100-value cap + hasMore + dedup/sort.
func TestFilterCompletions_Cap(t *testing.T) {
	t.Parallel()
	candidates := make([]string, 0, 150)
	for i := 0; i < 150; i++ {
		// zero-padded so lexical sort is deterministic
		candidates = append(candidates, "lensxxx"+pad(i))
	}
	candidates = append(candidates, "lensxxx000") // duplicate of i=0
	values, total, hasMore := filterCompletions(candidates, "lens")
	if total != 150 {
		t.Fatalf("total=%d, want 150 (dup collapsed)", total)
	}
	if len(values) != maxCompletionValues || !hasMore {
		t.Fatalf("len=%d hasMore=%v, want %d + true", len(values), hasMore, maxCompletionValues)
	}
}

func pad(i int) string {
	const digits = "0123456789"
	return string([]byte{digits[(i/100)%10], digits[(i/10)%10], digits[i%10]})
}
