package config_test

import (
	"strings"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/config"
)

// fakeEnv builds a getenv function backed by a map.
func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := config.Load(fakeEnv(map[string]string{"APP_ENV": "dev"}))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.AppEnv != "dev" {
		t.Errorf("AppEnv = %q, want %q", cfg.AppEnv, "dev")
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want default %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.OTelEnabled {
		t.Errorf("OTelEnabled = true, want false by default")
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	_, err := config.Load(fakeEnv(map[string]string{}))
	if err == nil {
		t.Fatal("Load succeeded with no APP_ENV; want fail-fast error")
	}
	if !strings.Contains(err.Error(), "APP_ENV") {
		t.Errorf("error %q does not name the missing required var APP_ENV", err.Error())
	}
}

func TestLoad_OTelSwitch(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want bool
	}{
		{"unset", "", false},
		{"true", "true", true},
		{"one", "1", true},
		{"false", "false", false},
		{"garbage", "maybe", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Load(fakeEnv(map[string]string{
				"APP_ENV":      "dev",
				"OTEL_ENABLED": tc.val,
			}))
			if err != nil {
				t.Fatalf("Load error: %v", err)
			}
			if cfg.OTelEnabled != tc.want {
				t.Errorf("OTEL_ENABLED=%q ⇒ OTelEnabled=%v, want %v", tc.val, cfg.OTelEnabled, tc.want)
			}
		})
	}
}

// TestSpotlightDisabledWhenUnset proves Sentry/Spotlight is off unless
// SENTRY_SPOTLIGHT is explicitly set (dk-p0-monorepo.md §8; never in CI).
func TestSpotlightDisabledWhenUnset(t *testing.T) {
	cfg, err := config.Load(fakeEnv(map[string]string{"APP_ENV": "dev"}))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.SpotlightEnabled() {
		t.Fatal("SpotlightEnabled() = true with SENTRY_SPOTLIGHT unset; must be disabled")
	}

	cfg2, err := config.Load(fakeEnv(map[string]string{
		"APP_ENV":          "dev",
		"SENTRY_SPOTLIGHT": "http://localhost:8969/stream",
	}))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if !cfg2.SpotlightEnabled() {
		t.Fatal("SpotlightEnabled() = false with SENTRY_SPOTLIGHT set; must be enabled")
	}
}

// TestChatKillSwitchDefaultsOff proves chat is NOT killed by default: an absent
// CHAT_KILL_SWITCH / CHAT_KILL_SWITCH_ACCOUNTS leaves the global flag false and
// the account list empty (CHAT-009). The switch is opt-in, never a silent kill.
func TestChatKillSwitchDefaultsOff(t *testing.T) {
	cfg, err := config.Load(fakeEnv(map[string]string{"APP_ENV": "dev"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatKillSwitchGlobal {
		t.Error("ChatKillSwitchGlobal = true by default; must be false (opt-in)")
	}
	if len(cfg.ChatKillSwitchAccounts) != 0 {
		t.Errorf("ChatKillSwitchAccounts = %v, want empty by default", cfg.ChatKillSwitchAccounts)
	}
	if cfg.LLMServiceBaseURL != "" || cfg.LLMGatewayToken != "" {
		t.Error("LLM plane config must be empty by default (fail closed)")
	}
}

// TestChatKillSwitchParses proves the global flag and comma-separated account
// list parse, trimming blanks.
func TestChatKillSwitchParses(t *testing.T) {
	cfg, err := config.Load(fakeEnv(map[string]string{
		"APP_ENV":                   "dev",
		"CHAT_KILL_SWITCH":          "true",
		"CHAT_KILL_SWITCH_ACCOUNTS": " a , ,b ",
		"LLM_SERVICE_URL":           "http://llm:9000",
		"LLM_GATEWAY_TOKEN":         "secret",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.ChatKillSwitchGlobal {
		t.Error("ChatKillSwitchGlobal = false, want true")
	}
	if len(cfg.ChatKillSwitchAccounts) != 2 || cfg.ChatKillSwitchAccounts[0] != "a" || cfg.ChatKillSwitchAccounts[1] != "b" {
		t.Errorf("ChatKillSwitchAccounts = %v, want [a b]", cfg.ChatKillSwitchAccounts)
	}
	if cfg.LLMServiceBaseURL != "http://llm:9000" || cfg.LLMGatewayToken != "secret" {
		t.Error("LLM plane config did not parse")
	}
}
