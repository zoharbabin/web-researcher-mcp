package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
)

// budgetEnvelope is a full-mode envelope as it is cached: the whole cleaned
// page plus fields that describe the page.
func budgetEnvelope(text string, structuredBytes int) map[string]any {
	out := map[string]any{
		"url":             "https://example.com/a",
		"content":         text,
		"contentType":     "html",
		"trust":           untrustedContentTrust,
		"contentLength":   len(text),
		"truncated":       false,
		"estimatedTokens": content.EstimateTokens(text),
		"sizeCategory":    content.SizeCategory(len(text)),
		"citation": map[string]any{
			"url":       "https://example.com/a",
			"formatted": map[string]any{"apa": strings.Repeat("Example, A. (2026). Title. ", 8)},
		},
		"wordCount": 5000,
	}
	if structuredBytes > 0 {
		out["structuredData"] = map[string]any{"jsonLd": []any{strings.Repeat("x", structuredBytes)}}
	}
	return out
}

// hostileText builds about n bytes of text that stays a valid sentence stream,
// so the cut falls on a boundary, but grows a lot when JSON escapes it.
func hostileText(kind string, n int) string {
	unit := map[string]string{
		"ascii":     "Plain words make a sentence here. ",
		"quotes":    `He said "stop" and "go" again. `,
		"backslash": `C:\dir\file and \n text. `,
		"nonascii":  "données 日本語 über – ñ. ",
		"control":   "tab\x01\x02\x03 bell\x07 end. ",
		"html":      "a < b && c > d <tag>. ",
	}[kind]
	// Repeat whole units so the text is valid UTF-8, then cut at a unit.
	return strings.Repeat(unit, n/len(unit)+1)[:n/len(unit)*len(unit)]
}

func cloneEnvelope(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFitScrapePage_StaysUnderBudgetAndPagesWithoutGap(t *testing.T) {
	t.Parallel()
	kinds := []string{"ascii", "quotes", "backslash", "nonascii", "control", "html"}
	for _, kind := range kinds {
		for _, maxLength := range []int{30000, 34000, 120000, maxScrapeLength} {
			t.Run(fmt.Sprintf("%s/%d", kind, maxLength), func(t *testing.T) {
				t.Parallel()
				full := hostileText(kind, 200_000)
				env := budgetEnvelope(full, 0)

				var joined strings.Builder
				offset := 0
				for pages := 0; ; pages++ {
					if pages > 200 {
						t.Fatal("paging did not finish")
					}
					b, err := fitScrapePage(cloneEnvelope(t, env), offset, maxLength, "")
					if err != nil {
						t.Fatal(err)
					}
					if len(b) > maxInlineResultBytes {
						t.Fatalf("offset %d: result is %d bytes, want <= %d", offset, len(b), maxInlineResultBytes)
					}
					var out map[string]any
					if err := json.Unmarshal(b, &out); err != nil {
						t.Fatal(err)
					}
					page := out["content"].(string)
					next, more := out["nextOffset"]
					if out["truncated"] != more {
						t.Fatalf("truncated=%v but nextOffset present=%v", out["truncated"], more)
					}
					if !more {
						if _, has := out["maxLengthApplied"]; has {
							t.Error("last part: maxLengthApplied must be omitted")
						}
						joined.WriteString(page)
						break
					}
					if applied, has := out["maxLengthApplied"]; has && int(applied.(float64)) >= maxLength {
						t.Errorf("maxLengthApplied = %v, want below max_length %d", applied, maxLength)
					}
					joined.WriteString(offsetMarkerRe.ReplaceAllString(page, ""))
					offset = int(next.(float64))
				}
				if joined.String() != full {
					t.Errorf("joined parts differ from the page (len %d vs %d)", joined.Len(), len(full))
				}
			})
		}
	}
}

func TestFitScrapePage_ClampReportsAppliedCap(t *testing.T) {
	t.Parallel()
	full := hostileText("ascii", 200_000)

	b, err := fitScrapePage(budgetEnvelope(full, 0), 0, 120_000, "")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	applied, ok := out["maxLengthApplied"].(float64)
	if !ok || applied <= 0 || applied >= 120_000 {
		t.Fatalf("maxLengthApplied = %v, want between 0 and 120000", out["maxLengthApplied"])
	}
	if out["truncated"] != true || out["nextOffset"] == nil {
		t.Errorf("truncated=%v nextOffset=%v, want true and present", out["truncated"], out["nextOffset"])
	}
	if got := int(out["contentLength"].(float64)); float64(got) > applied+continuationMarkerReserve {
		t.Errorf("contentLength %d exceeds maxLengthApplied %v plus the marker", got, applied)
	}
}

func TestFitScrapePage_SmallPageIsUnchanged(t *testing.T) {
	t.Parallel()
	full := hostileText("ascii", 5000)
	for _, maxLength := range []int{30000, 120000} {
		b, err := fitScrapePage(budgetEnvelope(full, 0), 0, maxLength, "")
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if out["content"] != full {
			t.Errorf("max_length %d: content changed", maxLength)
		}
		if _, has := out["maxLengthApplied"]; has {
			t.Errorf("max_length %d: maxLengthApplied must be omitted", maxLength)
		}
		if out["truncated"] != false {
			t.Errorf("max_length %d: truncated = %v, want false", maxLength, out["truncated"])
		}
	}
}

func TestFitScrapePage_LargeStructuredDataIsDroppedToKeepRoom(t *testing.T) {
	t.Parallel()
	full := hostileText("ascii", 100_000)

	b, err := fitScrapePage(budgetEnvelope(full, 60_000), 0, 30000, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > maxInlineResultBytes {
		t.Fatalf("result is %d bytes, want <= %d", len(b), maxInlineResultBytes)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if _, has := out["structuredData"]; has {
		t.Error("a 60 KB structuredData must be dropped from this response")
	}
	if got := int(out["contentLength"].(float64)); got < minInlineContentBytes {
		t.Errorf("contentLength = %d, want at least %d", got, minInlineContentBytes)
	}

	// Small structured data stays.
	b, err = fitScrapePage(budgetEnvelope(full, 2_000), 0, 30000, "")
	if err != nil {
		t.Fatal(err)
	}
	out = nil
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if _, has := out["structuredData"]; !has {
		t.Error("a 2 KB structuredData must stay")
	}
}

// hostilePageHandler serves a page of about size bytes whose text is full of
// quotes, backslashes and non-ASCII characters.
func hostilePageHandler(hits *atomic.Int64, size int) http.HandlerFunc {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><title>Hostile page</title></head><body><article><h1>Hostile page</h1>")
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, `<p>Item %d: "quoted" text with C:\dir\file%d and données 日本語 über – ñ "again" "and again" here.</p>`, i, i)
	}
	b.WriteString("</article></body></html>")
	page := b.String()
	return func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}
}

func TestScrapePage_ResultStaysUnderBudget(t *testing.T) {
	var hits atomic.Int64
	big := httptest.NewServer(longArticleHandler(&hits))
	defer big.Close()
	hostile := httptest.NewServer(hostilePageHandler(&hits, 200_000))
	defer hostile.Close()
	ctx, session := scrapeOffsetSession(t, big)

	tests := []struct {
		name string
		url  string
		args map[string]any
	}{
		{"default max_length", big.URL, nil},
		{"max_length 40000", big.URL, map[string]any{"max_length": 40000}},
		{"max_length 120000", big.URL, map[string]any{"max_length": 120000}},
		{"escape-heavy default", hostile.URL, nil},
		{"escape-heavy max_length 120000", hostile.URL, map[string]any{"max_length": 120000}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"url": tc.url}
			for k, v := range tc.args {
				args[k] = v
			}
			out, res := callScrape(ctx, t, session, args)
			if out == nil {
				t.Fatalf("tool error: %v", res.Content)
			}
			text := res.Content[0].(*mcp.TextContent).Text
			if len(text) > maxInlineResultBytes {
				t.Errorf("serialized result is %d bytes, want <= %d", len(text), maxInlineResultBytes)
			}
			if out["truncated"] != true || out["nextOffset"] == nil {
				t.Errorf("truncated=%v nextOffset=%v, want a cut page", out["truncated"], out["nextOffset"])
			}
			if tc.args["max_length"] != nil && tc.args["max_length"].(int) > maxInlineResultBytes {
				if out["maxLengthApplied"] == nil {
					t.Error("a clamped page must report maxLengthApplied")
				}
			}
		})
	}
}

func TestScrapePage_ClampedPagesJoinWithoutGap(t *testing.T) {
	var hits atomic.Int64
	ts := httptest.NewServer(hostilePageHandler(&hits, 150_000))
	defer ts.Close()
	ctx, session := scrapeOffsetSession(t, ts)

	// Reference: small parts that never hit the budget.
	want, total, _ := readAllParts(ctx, t, session, ts.URL, 5000)
	// Clamped parts: max_length is far above what the budget allows.
	got, gotTotal, pages := readAllParts(ctx, t, session, ts.URL, 500_000)
	if pages < 3 {
		t.Errorf("read in %d parts, want at least 3", pages)
	}
	if total != gotTotal || len(want) != total {
		t.Fatalf("totalLength %d / %d, reference length %d", total, gotTotal, len(want))
	}
	if got != want {
		t.Errorf("clamped parts differ from the reference (len %d vs %d)", len(got), len(want))
	}
}

func TestScrapePage_SmallPageHasNoBudgetFields(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><title>Small</title></head><body><article><h1>Small</h1><p>One short paragraph about a topic that is long enough to count as prose for the page.</p></article></body></html>"))
	}))
	defer ts.Close()
	ctx, session := scrapeOffsetSession(t, ts)

	for _, args := range []map[string]any{{"url": ts.URL}, {"url": ts.URL, "max_length": 120000}} {
		out, _ := callScrape(ctx, t, session, args)
		if _, has := out["maxLengthApplied"]; has {
			t.Errorf("%v: maxLengthApplied must be omitted for a page that fits", args)
		}
		if out["truncated"] != false {
			t.Errorf("%v: truncated = %v, want false", args, out["truncated"])
		}
	}
}
