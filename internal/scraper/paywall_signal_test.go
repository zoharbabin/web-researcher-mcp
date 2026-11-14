package scraper

import (
	"encoding/json"
	"testing"
)

func TestJSONLDAccess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		raw                string
		wantFree, wantPaid bool
	}{
		{"bool false", `{"@type":"NewsArticle","isAccessibleForFree":false}`, false, true},
		{"bool true", `{"@type":"NewsArticle","isAccessibleForFree":true}`, true, false},
		{"string False", `{"isAccessibleForFree":"False"}`, false, true},
		{"string true", `{"isAccessibleForFree":" true "}`, true, false},
		{"schema.org URL false", `{"isAccessibleForFree":"https://schema.org/False"}`, false, true},
		{"schema.org URL true", `{"isAccessibleForFree":"https://schema.org/True"}`, true, false},
		{"in @graph", `{"@graph":[{"@type":"WebSite"},{"@type":"Article","isAccessibleForFree":false}]}`, false, true},
		{"in hasPart", `{"@type":"Article","isAccessibleForFree":true,"hasPart":{"@type":"WebPageElement","isAccessibleForFree":false,"cssSelector":".body"}}`, true, true},
		{"top-level array", `[{"@type":"Person"},{"@type":"Article","isAccessibleForFree":false}]`, false, true},
		{"absent", `{"@type":"Article","headline":"x"}`, false, false},
		{"unknown string", `{"isAccessibleForFree":"maybe"}`, false, false},
		{"wrong type", `{"isAccessibleForFree":1}`, false, false},
		{"malformed", `{"isAccessibleForFree":`, false, false},
		{"scalar", `"x"`, false, false},
		{"null", `null`, false, false},
	}
	for _, tt := range tests {
		free, paid := jsonLDAccess(json.RawMessage(tt.raw))
		if free != tt.wantFree || paid != tt.wantPaid {
			t.Errorf("%s: got free=%v paywalled=%v, want %v/%v", tt.name, free, paid, tt.wantFree, tt.wantPaid)
		}
	}
}

func TestSignalsAccessibleForFree(t *testing.T) {
	t.Parallel()
	ld := func(blocks ...string) *StructuredData {
		sd := &StructuredData{}
		for _, b := range blocks {
			sd.JSONLD = append(sd.JSONLD, json.RawMessage(b))
		}
		return sd
	}
	tests := []struct {
		name string
		sd   *StructuredData
		want string // "", "free" or "paywalled"
	}{
		{"nil data", nil, ""},
		{"no markup", ld(`{"@type":"Article"}`), ""},
		{"free", ld(`{"isAccessibleForFree":true}`), "free"},
		{"paywalled", ld(`{"isAccessibleForFree":false}`), "paywalled"},
		{"paywalled wins across blocks", ld(`{"isAccessibleForFree":true}`, `{"isAccessibleForFree":false}`), "paywalled"},
		{"free in one block, none in other", ld(`{"@type":"WebSite"}`, `{"isAccessibleForFree":true}`), "free"},
	}
	for _, tt := range tests {
		got := ""
		if v := tt.sd.Signals().AccessibleForFree; v != nil {
			got = map[bool]string{true: "free", false: "paywalled"}[*v]
		}
		if got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
