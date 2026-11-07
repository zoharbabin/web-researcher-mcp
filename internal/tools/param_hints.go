package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// paramAliases maps parameter names that agents commonly guess to the real
// parameter of the same tool. The SDK rejects unknown properties before the
// handler runs, with a message that does not name the valid field. The
// middleware below appends a hint, so the agent fixes the call in one retry.
// Schemas stay unchanged: an alias property would cost tokens on every load.
var paramAliases = map[string]map[string]string{
	"web_search":  {"max_results": "num_results", "limit": "num_results"},
	"news_search": {"max_results": "num_results", "limit": "num_results"},
}

// unexpectedPropsRe matches the SDK's schema-validation message and captures
// the quoted property list.
var unexpectedPropsRe = regexp.MustCompile(`unexpected additional properties \[([^\]]*)\]`)

// registerParamAliasHints adds a receiving middleware that rewrites the SDK's
// "unexpected additional properties" tool error to name the valid parameter.
// Only error results of tools/call that contain the SDK message are touched.
func registerParamAliasHints(srv *mcp.Server) {
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if method != "tools/call" || err != nil {
				return res, err
			}
			call, ok := req.(*mcp.CallToolRequest)
			result, isResult := res.(*mcp.CallToolResult)
			if !ok || !isResult || result == nil || !result.IsError {
				return res, err
			}
			addAliasHint(call.Params.Name, result)
			return res, err
		}
	})
}

// addAliasHint appends "Did you mean ..." to the validation error text when
// the rejected property has a known alias for this tool.
func addAliasHint(tool string, result *mcp.CallToolResult) {
	aliases := paramAliases[tool]
	if len(aliases) == 0 {
		return
	}
	for i, c := range result.Content {
		tc, ok := c.(*mcp.TextContent)
		if !ok {
			continue
		}
		m := unexpectedPropsRe.FindStringSubmatch(tc.Text)
		if m == nil {
			continue
		}
		var hints []string
		for _, quoted := range strings.Split(m[1], ",") {
			name := strings.Trim(strings.TrimSpace(quoted), `"`)
			if want, found := aliases[name]; found {
				hints = append(hints, fmt.Sprintf("%q is not a parameter of %s. Use %q.", name, tool, want))
			}
		}
		if len(hints) > 0 {
			result.Content[i] = &mcp.TextContent{Text: tc.Text + " " + strings.Join(hints, " ")}
		}
	}
}
