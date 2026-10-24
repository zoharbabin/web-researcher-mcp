package config

import (
	"strings"
	"testing"
	"time"
)

const (
	vaultKeyA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	vaultKeyB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// clearVaultTTLEnv blanks the TTL variables so default-value assertions do not
// depend on the developer's shell. An empty value falls back to the default.
func clearVaultTTLEnv(t *testing.T) {
	t.Helper()
	t.Setenv("COOKIE_VAULT_TTL", "")
	t.Setenv("COOKIE_VAULT_PROFILE_TTL", "")
}

// TestConfigCookieVaultKeyValidation pins the cookie-vault config contract
// (see issue #241): the vault is off by default and needs no key then; when on,
// the key must be 64 hex, distinct from the cache keys, and the bounds hold.
func TestConfigCookieVaultKeyValidation(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string // substring; empty means Load must succeed
	}{
		{"disabled needs no key", map[string]string{}, ""},
		{"disabled ignores bad key", map[string]string{"COOKIE_VAULT_ENCRYPTION_KEY": "nothex"}, ""},
		{"enabled without key", map[string]string{"COOKIE_VAULT_ENABLED": "true"}, "COOKIE_VAULT_ENCRYPTION_KEY is required"},
		{"enabled with short key", map[string]string{"COOKIE_VAULT_ENABLED": "true", "COOKIE_VAULT_ENCRYPTION_KEY": "abc"}, "64 hex"},
		{"enabled with non-hex key", map[string]string{"COOKIE_VAULT_ENABLED": "true", "COOKIE_VAULT_ENCRYPTION_KEY": strings.Repeat("z", 64)}, "64 hex"},
		{"same as cache key", map[string]string{"COOKIE_VAULT_ENABLED": "true", "COOKIE_VAULT_ENCRYPTION_KEY": vaultKeyA, "CACHE_ENCRYPTION_KEY": vaultKeyA}, "must differ from CACHE_ENCRYPTION_KEY"},
		{"same as cache key, case-insensitive", map[string]string{"COOKIE_VAULT_ENABLED": "true", "COOKIE_VAULT_ENCRYPTION_KEY": strings.ToUpper(vaultKeyA), "CACHE_ENCRYPTION_KEY": vaultKeyA}, "must differ from CACHE_ENCRYPTION_KEY"},
		{"same as previous cache key", map[string]string{"COOKIE_VAULT_ENABLED": "true", "COOKIE_VAULT_ENCRYPTION_KEY": vaultKeyA, "CACHE_ENCRYPTION_KEY_PREV": vaultKeyA}, "must differ from CACHE_ENCRYPTION_KEY"},
		{"valid distinct key", map[string]string{"COOKIE_VAULT_ENABLED": "true", "COOKIE_VAULT_ENCRYPTION_KEY": vaultKeyA, "CACHE_ENCRYPTION_KEY": vaultKeyB}, ""},
		{"ttl above cap", map[string]string{"COOKIE_VAULT_ENABLED": "true", "COOKIE_VAULT_ENCRYPTION_KEY": vaultKeyA, "COOKIE_VAULT_TTL": "721h"}, "COOKIE_VAULT_TTL"},
		{"captures above cap", map[string]string{"COOKIE_VAULT_ENABLED": "true", "COOKIE_VAULT_ENCRYPTION_KEY": vaultKeyA, "MAX_CONCURRENT_CAPTURES": "6"}, "MAX_CONCURRENT_CAPTURES"},
		{"capture timeout too long", map[string]string{"COOKIE_VAULT_ENABLED": "true", "COOKIE_VAULT_ENCRYPTION_KEY": vaultKeyA, "COOKIE_CAPTURE_TIMEOUT": "16m"}, "COOKIE_CAPTURE_TIMEOUT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v (cfg=%v)", tc.wantErr, err, cfg != nil)
			}
		})
	}

	t.Run("defaults and consent activation", func(t *testing.T) {
		setRequiredEnv(t)
		clearVaultTTLEnv(t)
		t.Setenv("COOKIE_VAULT_ENABLED", "true")
		t.Setenv("COOKIE_VAULT_ENCRYPTION_KEY", vaultKeyA)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.CookieVaultTTL != 24*time.Hour || cfg.MaxConcurrentCaptures != 1 || cfg.CookieCaptureTimeout != 5*time.Minute {
			t.Errorf("unexpected defaults: ttl=%v captures=%d timeout=%v", cfg.CookieVaultTTL, cfg.MaxConcurrentCaptures, cfg.CookieCaptureTimeout)
		}
		if !cfg.Features.RegulatedEnabled() {
			t.Error("cookie vault must activate the consent subsystem")
		}
	})
}

// TestConfigCookieVaultStdioConsent pins the STDIO consent opt-in (#241): off by
// default, and honored only with the vault on, a valid STDIO_USER_ID and no PORT.
func TestConfigCookieVaultStdioConsent(t *testing.T) {
	base := map[string]string{"COOKIE_VAULT_ENABLED": "true", "COOKIE_VAULT_ENCRYPTION_KEY": vaultKeyA, "STDIO_USER_ID": "alice"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name     string
		env      map[string]string
		want     bool
		wantWarn bool
	}{
		{"default off", with(nil), false, false},
		{"explicit opt-in", with(map[string]string{"COOKIE_VAULT_STDIO_CONSENT": "true"}), true, false},
		{"vault disabled", with(map[string]string{"COOKIE_VAULT_ENABLED": "false", "COOKIE_VAULT_STDIO_CONSENT": "true"}), false, true},
		{"no stdio user", with(map[string]string{"STDIO_USER_ID": "", "COOKIE_VAULT_STDIO_CONSENT": "true"}), false, true},
		{"http mode", with(map[string]string{"PORT": "8080", "COOKIE_VAULT_STDIO_CONSENT": "true"}), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.CookieVaultStdioConsent != tc.want {
				t.Fatalf("CookieVaultStdioConsent = %v, want %v", cfg.CookieVaultStdioConsent, tc.want)
			}
		})
	}
}

// TestConfigProfileTTL pins COOKIE_VAULT_PROFILE_TTL (issue #775): one week by
// default, bounded to 720h, checked only when the vault is on.
func TestConfigProfileTTL(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		want    time.Duration
		wantErr string
	}{
		{"default", nil, 7 * 24 * time.Hour, ""},
		{"custom", map[string]string{"COOKIE_VAULT_PROFILE_TTL": "48h"}, 48 * time.Hour, ""},
		{"above cap", map[string]string{"COOKIE_VAULT_PROFILE_TTL": "721h"}, 0, "COOKIE_VAULT_PROFILE_TTL"},
		{"disabled vault ignores it", map[string]string{"COOKIE_VAULT_ENABLED": "false", "COOKIE_VAULT_PROFILE_TTL": "721h"}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			clearVaultTTLEnv(t)
			t.Setenv("COOKIE_VAULT_ENABLED", "true")
			t.Setenv("COOKIE_VAULT_ENCRYPTION_KEY", vaultKeyA)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != 0 && cfg.CookieVaultProfileTTL != tc.want {
				t.Fatalf("CookieVaultProfileTTL = %v, want %v", cfg.CookieVaultProfileTTL, tc.want)
			}
		})
	}
}

// TestConfigProfileMaxTotalMB pins COOKIE_VAULT_PROFILE_MAX_TOTAL_MB (issue
// #775): 2048 by default, bounded to 64..102400, checked only when the vault is on.
func TestConfigProfileMaxTotalMB(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		want    int
		wantErr string
	}{
		{"default", nil, 2048, ""},
		{"custom", map[string]string{"COOKIE_VAULT_PROFILE_MAX_TOTAL_MB": "512"}, 512, ""},
		{"too small", map[string]string{"COOKIE_VAULT_PROFILE_MAX_TOTAL_MB": "10"}, 0, "COOKIE_VAULT_PROFILE_MAX_TOTAL_MB"},
		{"too big", map[string]string{"COOKIE_VAULT_PROFILE_MAX_TOTAL_MB": "102401"}, 0, "COOKIE_VAULT_PROFILE_MAX_TOTAL_MB"},
		{"disabled vault ignores it", map[string]string{"COOKIE_VAULT_ENABLED": "false", "COOKIE_VAULT_PROFILE_MAX_TOTAL_MB": "10"}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("COOKIE_VAULT_ENABLED", "true")
			t.Setenv("COOKIE_VAULT_ENCRYPTION_KEY", vaultKeyA)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != 0 && cfg.CookieVaultProfileMaxTotalMB != tc.want {
				t.Fatalf("CookieVaultProfileMaxTotalMB = %d, want %d", cfg.CookieVaultProfileMaxTotalMB, tc.want)
			}
		})
	}
}

// TestConfigCookieCaptureInHTTP pins the HTTP-mode capture opt-in (issue #775):
// off by default, and honored only with the vault on and a display present.
func TestConfigCookieCaptureInHTTP(t *testing.T) {
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want bool
	}{
		{"default off", "darwin", nil, false},
		{"opt-in on macOS", "darwin", map[string]string{"COOKIE_CAPTURE_IN_HTTP": "true"}, true},
		{"opt-in on Linux with DISPLAY", "linux", map[string]string{"COOKIE_CAPTURE_IN_HTTP": "true", "DISPLAY": ":0"}, true},
		{"opt-in on Linux with Wayland", "linux", map[string]string{"COOKIE_CAPTURE_IN_HTTP": "true", "WAYLAND_DISPLAY": "wayland-0"}, true},
		{"headless Linux", "linux", map[string]string{"COOKIE_CAPTURE_IN_HTTP": "true", "DISPLAY": "", "WAYLAND_DISPLAY": ""}, false},
		{"vault disabled", "darwin", map[string]string{"COOKIE_CAPTURE_IN_HTTP": "true", "COOKIE_VAULT_ENABLED": "false"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := hostGOOS
			hostGOOS = tc.goos
			t.Cleanup(func() { hostGOOS = orig })
			setRequiredEnv(t)
			t.Setenv("COOKIE_VAULT_ENABLED", "true")
			t.Setenv("COOKIE_VAULT_ENCRYPTION_KEY", vaultKeyA)
			t.Setenv("PORT", "8080")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.CookieCaptureInHTTP != tc.want {
				t.Fatalf("CookieCaptureInHTTP = %v, want %v", cfg.CookieCaptureInHTTP, tc.want)
			}
		})
	}
}
