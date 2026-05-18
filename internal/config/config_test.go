package config

import (
	"log/slog"
	"strings"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "test-api-key")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "test-cx-id")
}

func TestLoadWithRequiredEnvVars(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error with required vars set, got: %v", err)
	}
	if cfg.GoogleAPIKey != "test-api-key" {
		t.Errorf("expected GoogleAPIKey=test-api-key, got %s", cfg.GoogleAPIKey)
	}
	if cfg.GoogleCX != "test-cx-id" {
		t.Errorf("expected GoogleCX=test-cx-id, got %s", cfg.GoogleCX)
	}
}

func TestLoadMissingGoogleAPIKey(t *testing.T) {
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "test-cx-id")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when GOOGLE_CUSTOM_SEARCH_API_KEY is missing")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CUSTOM_SEARCH_API_KEY is required") {
		t.Errorf("expected error about missing API key, got: %v", err)
	}
}

func TestLoadMissingGoogleCX(t *testing.T) {
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "test-key")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when GOOGLE_CUSTOM_SEARCH_ID is missing")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CUSTOM_SEARCH_ID is required") {
		t.Errorf("expected error about missing CX, got: %v", err)
	}
}

func TestLoadMissingBothRequired(t *testing.T) {
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when both required vars are missing")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CUSTOM_SEARCH_API_KEY is required") {
		t.Errorf("expected error about API key, got: %v", err)
	}
	if !strings.Contains(err.Error(), "GOOGLE_CUSTOM_SEARCH_ID is required") {
		t.Errorf("expected error about CX, got: %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Search.Provider != "google" {
		t.Errorf("expected default provider=google, got %s", cfg.Search.Provider)
	}
	if cfg.Port != 0 {
		t.Errorf("expected default port=0, got %d", cfg.Port)
	}
	if cfg.CacheDir != "./cache" {
		t.Errorf("expected default CacheDir=./cache, got %s", cfg.CacheDir)
	}
	if cfg.CacheMaxMemoryMB != 64 {
		t.Errorf("expected default CacheMaxMemoryMB=64, got %d", cfg.CacheMaxMemoryMB)
	}
	if cfg.RateLimit.PerTenant != 30 {
		t.Errorf("expected default PerTenant=30, got %d", cfg.RateLimit.PerTenant)
	}
	if cfg.RateLimit.Global != 1000 {
		t.Errorf("expected default Global=1000, got %d", cfg.RateLimit.Global)
	}
	if cfg.RateLimit.DailyQuota != 1000 {
		t.Errorf("expected default DailyQuota=1000, got %d", cfg.RateLimit.DailyQuota)
	}
	if cfg.MaxScrapeConcurrency != 5 {
		t.Errorf("expected default MaxScrapeConcurrency=5, got %d", cfg.MaxScrapeConcurrency)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("expected default LogFormat=json, got %s", cfg.LogFormat)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected default LogLevel=Info, got %v", cfg.LogLevel)
	}
	if !cfg.MetricsEnabled {
		t.Error("expected default MetricsEnabled=true")
	}
	if cfg.AllowPrivateIPs {
		t.Error("expected default AllowPrivateIPs=false")
	}
	if cfg.CacheIsolation != "shared" {
		t.Errorf("expected default CacheIsolation=shared, got %s", cfg.CacheIsolation)
	}
}

func setCustomEnv(t *testing.T) {
	t.Helper()
	setRequiredEnv(t)
	envVars := map[string]string{
		"SEARCH_PROVIDER":        "brave",
		"BRAVE_API_KEY":          "brave-key-123",
		"PORT":                   "8080",
		"CACHE_DIR":              "/tmp/cache",
		"CACHE_MAX_MEMORY_MB":    "128",
		"RATE_LIMIT_PER_TENANT":  "60",
		"RATE_LIMIT_GLOBAL":      "2000",
		"DAILY_QUOTA_PER_TENANT": "5000",
		"MAX_SCRAPE_CONCURRENCY": "10",
		"LOG_LEVEL":              "debug",
		"LOG_FORMAT":             "text",
		"METRICS_ENABLED":        "false",
		"ALLOW_PRIVATE_IPS":      "true",
		"ALLOWED_ORIGINS":        "http://localhost:3000, https://example.com",
		"ALLOWED_DOMAINS":        "example.com,test.org",
		"REDIS_URL":              "redis://localhost:6379",
		"CHROME_PATH":            "/usr/bin/chromium",
		"OAUTH_ISSUER_URL":       "https://auth.example.com",
		"OAUTH_AUDIENCE":         "my-audience",
	}
	for k, v := range envVars {
		t.Setenv(k, v)
	}
}

func TestLoadCustomSearchProvider(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Search.Provider != "brave" {
		t.Errorf("expected provider=brave, got %s", cfg.Search.Provider)
	}
	if cfg.Search.BraveAPIKey != "brave-key-123" {
		t.Errorf("expected BraveAPIKey=brave-key-123, got %s", cfg.Search.BraveAPIKey)
	}
}

func TestLoadCustomServerConfig(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected port=8080, got %d", cfg.Port)
	}
}

func TestLoadCustomCacheConfig(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CacheDir != "/tmp/cache" {
		t.Errorf("expected CacheDir=/tmp/cache, got %s", cfg.CacheDir)
	}
	if cfg.CacheMaxMemoryMB != 128 {
		t.Errorf("expected CacheMaxMemoryMB=128, got %d", cfg.CacheMaxMemoryMB)
	}
	if cfg.RedisURL != "redis://localhost:6379" {
		t.Errorf("expected RedisURL=redis://localhost:6379, got %s", cfg.RedisURL)
	}
}

func TestLoadCustomRateLimits(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateLimit.PerTenant != 60 {
		t.Errorf("expected PerTenant=60, got %d", cfg.RateLimit.PerTenant)
	}
	if cfg.RateLimit.Global != 2000 {
		t.Errorf("expected Global=2000, got %d", cfg.RateLimit.Global)
	}
	if cfg.RateLimit.DailyQuota != 5000 {
		t.Errorf("expected DailyQuota=5000, got %d", cfg.RateLimit.DailyQuota)
	}
}

func TestLoadCustomScrapingConfig(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxScrapeConcurrency != 10 {
		t.Errorf("expected MaxScrapeConcurrency=10, got %d", cfg.MaxScrapeConcurrency)
	}
	if cfg.ChromePath != "/usr/bin/chromium" {
		t.Errorf("expected ChromePath=/usr/bin/chromium, got %s", cfg.ChromePath)
	}
	if !cfg.AllowPrivateIPs {
		t.Error("expected AllowPrivateIPs=true")
	}
}

func TestLoadCustomObservability(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("expected LogLevel=Debug, got %v", cfg.LogLevel)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("expected LogFormat=text, got %s", cfg.LogFormat)
	}
	if cfg.MetricsEnabled {
		t.Error("expected MetricsEnabled=false")
	}
}

func TestLoadCustomNetworkConfig(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "http://localhost:3000" || cfg.AllowedOrigins[1] != "https://example.com" {
		t.Errorf("unexpected AllowedOrigins: %v", cfg.AllowedOrigins)
	}
	if len(cfg.AllowedDomains) != 2 || cfg.AllowedDomains[0] != "example.com" || cfg.AllowedDomains[1] != "test.org" {
		t.Errorf("unexpected AllowedDomains: %v", cfg.AllowedDomains)
	}
}

func TestLoadCustomOAuth(t *testing.T) {
	setCustomEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OAuth.IssuerURL != "https://auth.example.com" {
		t.Errorf("expected OAuth.IssuerURL=https://auth.example.com, got %s", cfg.OAuth.IssuerURL)
	}
	if cfg.OAuth.Audience != "my-audience" {
		t.Errorf("expected OAuth.Audience=my-audience, got %s", cfg.OAuth.Audience)
	}
}

func TestLoadBraveProviderMissingKey(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SEARCH_PROVIDER", "brave")
	t.Setenv("BRAVE_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SEARCH_PROVIDER=brave without BRAVE_API_KEY")
	}
	if !strings.Contains(err.Error(), "BRAVE_API_KEY is required") {
		t.Errorf("expected error about missing BRAVE_API_KEY, got: %v", err)
	}
}

func TestLoadSerperProviderMissingKey(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SEARCH_PROVIDER", "serper")
	t.Setenv("SERPER_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SEARCH_PROVIDER=serper without SERPER_API_KEY")
	}
	if !strings.Contains(err.Error(), "SERPER_API_KEY is required") {
		t.Errorf("expected error about missing SERPER_API_KEY, got: %v", err)
	}
}

func TestLoadSearXNGProviderMissingURL(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SEARCH_PROVIDER", "searxng")
	t.Setenv("SEARXNG_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SEARCH_PROVIDER=searxng without SEARXNG_URL")
	}
	if !strings.Contains(err.Error(), "SEARXNG_URL is required") {
		t.Errorf("expected error about missing SEARXNG_URL, got: %v", err)
	}
}

func TestLoadCacheEncryptionKeyValidation(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CACHE_ENCRYPTION_KEY", "tooshort")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid CACHE_ENCRYPTION_KEY length")
	}
	if !strings.Contains(err.Error(), "CACHE_ENCRYPTION_KEY must be exactly 64 hex characters") {
		t.Errorf("expected error about key length, got: %v", err)
	}
}

func TestLoadCacheEncryptionKeyValid(t *testing.T) {
	setRequiredEnv(t)
	validKey := strings.Repeat("ab", 32) // 64 hex chars
	t.Setenv("CACHE_ENCRYPTION_KEY", validKey)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error with valid encryption key: %v", err)
	}
	if cfg.CacheEncryptionKey != validKey {
		t.Errorf("expected encryption key to be set")
	}
}

func TestLogLevelParsing(t *testing.T) {
	tests := []struct {
		env      string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run("LOG_LEVEL="+tt.env, func(t *testing.T) {
			setRequiredEnv(t)
			if tt.env != "" {
				t.Setenv("LOG_LEVEL", tt.env)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.LogLevel != tt.expected {
				t.Errorf("for LOG_LEVEL=%q expected %v, got %v", tt.env, tt.expected, cfg.LogLevel)
			}
		})
	}
}

func TestEnvIntFallback(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PORT", "not-a-number")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 0 {
		t.Errorf("expected port=0 (fallback) for invalid int, got %d", cfg.Port)
	}
}

func TestSplitCSVEmpty(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ALLOWED_ORIGINS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AllowedOrigins != nil {
		t.Errorf("expected nil for empty CSV, got %v", cfg.AllowedOrigins)
	}
}

func TestSplitCSVTrimsSpaces(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ALLOWED_ORIGINS", "  http://a.com  ,  http://b.com  , , ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("expected 2 origins, got %d: %v", len(cfg.AllowedOrigins), cfg.AllowedOrigins)
	}
	if cfg.AllowedOrigins[0] != "http://a.com" {
		t.Errorf("expected first origin=http://a.com, got %s", cfg.AllowedOrigins[0])
	}
	if cfg.AllowedOrigins[1] != "http://b.com" {
		t.Errorf("expected second origin=http://b.com, got %s", cfg.AllowedOrigins[1])
	}
}

func TestConfigStillReturnedOnError(t *testing.T) {
	t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "")
	t.Setenv("GOOGLE_CUSTOM_SEARCH_ID", "")

	cfg, err := Load()
	if err == nil {
		t.Fatal("expected validation error")
	}
	// Config should still be returned (partial) even with errors
	if cfg == nil {
		t.Fatal("expected non-nil config even on error")
	}
}
