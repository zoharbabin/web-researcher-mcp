package config

import (
	"strings"
	"testing"
	"time"
)

// TestLoadScrapeTotalTimeout pins #853's env knob: default 90s, a valid
// duration is used as is, and a bad or non-positive value fails Load instead
// of silently falling back to the default.
func TestLoadScrapeTotalTimeout(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr string
	}{
		{"unset uses default", "", 90 * time.Second, ""},
		{"valid", "5s", 5 * time.Second, ""},
		{"valid minutes", "3m", 3 * time.Minute, ""},
		{"not a duration", "abc", 0, "SCRAPE_TOTAL_TIMEOUT must be a Go duration"},
		{"missing unit", "90", 0, "SCRAPE_TOTAL_TIMEOUT must be a Go duration"},
		{"zero", "0s", 0, "SCRAPE_TOTAL_TIMEOUT must be greater than 0"},
		{"negative", "-5s", 0, "SCRAPE_TOTAL_TIMEOUT must be greater than 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("SCRAPE_TOTAL_TIMEOUT", tc.value)
			cfg, err := Load()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.ScrapeTotalTimeout != tc.want {
				t.Errorf("ScrapeTotalTimeout = %s, want %s", cfg.ScrapeTotalTimeout, tc.want)
			}
		})
	}
}
