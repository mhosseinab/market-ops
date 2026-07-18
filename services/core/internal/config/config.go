// Package config loads the core service configuration from the process
// environment. Loading is fail-fast: every required variable that is absent or
// empty is collected and reported in a single error so a misconfigured
// deployment cannot start half-wired.
//
// Values are plain data — no locale, region, or currency branching lives here
// (CLAUDE.md localization boundary). Secrets are never defaulted; they are read
// from the environment only (dk-p0-monorepo.md §8).
package config

import (
	"fmt"
	"strings"
)

// ServiceName is the stable identity of this binary in logs and telemetry.
const ServiceName = "core"

// Config is the resolved, validated core configuration.
type Config struct {
	// AppEnv names the deployment environment ("dev", "prod", …). Required: it
	// tags structured logs and telemetry resources, so an unset value would
	// silently mislabel every signal.
	AppEnv string

	// HTTPAddr is the listen address for the HTTP API (default ":8080").
	HTTPAddr string

	// OTelEnabled turns on OpenTelemetry SDK wiring (OTEL_ENABLED). When false,
	// no exporter is created and the tracer provider stays the no-op default.
	OTelEnabled bool

	// Spotlight, when non-empty, enables dev-only Sentry Spotlight delivery
	// (SENTRY_SPOTLIGHT). It is either a truthy flag or the sidecar stream URL;
	// unset means all Sentry wiring stays disabled (dk-p0-monorepo.md §8).
	Spotlight string

	// ChatKillSwitchGlobal disables chat platform-wide when true
	// (CHAT_KILL_SWITCH global; CHAT-009). Screens stay fully functional.
	// Absent ⇒ false ⇒ chat is not globally killed.
	ChatKillSwitchGlobal bool

	// ChatKillSwitchAccounts is the set of marketplace-account ids for which
	// chat is disabled (CHAT_KILL_SWITCH_ACCOUNTS, comma-separated UUIDs;
	// CHAT-009). Screens for those accounts stay fully functional.
	ChatKillSwitchAccounts []string

	// LLMServiceBaseURL is the base URL of the internal Python LLM plane
	// (LLM_SERVICE_URL). Unset ⇒ /chat fails closed with a structured
	// provider_unavailable state; screens are unaffected (§19.3).
	LLMServiceBaseURL string

	// LLMGatewayToken is the read+Draft-only bearer credential the core mints
	// for the LLM plane (LLM_GATEWAY_TOKEN; PRD §8, §12.3, §19.3). Its capability
	// envelope is enforced by perm.GatewayCan. Unset ⇒ the LLM plane cannot call
	// back into read/Draft endpoints. A secret; never defaulted, never logged.
	LLMGatewayToken string

	// NotifySMTPAddr is the host:port of the SMTP server the daily email digest
	// sends through (SMTP_ADDR; default localhost:1025 — mailpit in dev, §19.3).
	NotifySMTPAddr string

	// NotifyFromAddr is the digest From address (NOTIFY_FROM_ADDR). Unset ⇒ the
	// daily digest job is NOT wired (a nil runner, no-op): the beta never sends
	// mail without an explicitly configured sender. In-app notifications and the
	// analytics pipe are unaffected.
	NotifyFromAddr string

	// AppBaseURL is the base URL the digest deep-links to the briefing from
	// (APP_BASE_URL; default http://localhost:5173). The email LINKS to the
	// briefing (§6.8); it never regenerates it.
	AppBaseURL string

	// NotifyLocale is the render locale for the email digest (NOTIFY_LOCALE;
	// default fa-IR). Locale is DATA — the digest selects a pack by this string,
	// never a code branch (LOC-001).
	NotifyLocale string

	// NotifyRegion is the region label stamped on analytics events emitted by core
	// jobs (NOTIFY_REGION; default IR). Plain data — never branched on.
	NotifyRegion string

	// CurrencyContractVersion is the §18 currency-contract-version envelope field
	// stamped on analytics events (CURRENCY_CONTRACT_VERSION; default v1). An
	// opaque version string; never used in money arithmetic.
	CurrencyContractVersion string
}

// SpotlightEnabled reports whether dev Spotlight wiring should be initialized.
// Unset SENTRY_SPOTLIGHT ⇒ false ⇒ Sentry is fully disabled.
func (c Config) SpotlightEnabled() bool { return c.Spotlight != "" }

// splitList parses a comma-separated env value into a trimmed, non-empty list.
// An empty or all-whitespace value yields nil (no entries).
func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Load reads configuration using getenv (pass os.Getenv in production; a fake in
// tests). It returns an error listing every missing required variable.
func Load(getenv func(string) string) (*Config, error) {
	r := reader{getenv: getenv}

	cfg := &Config{
		AppEnv:      r.required("APP_ENV"),
		HTTPAddr:    r.optional("HTTP_ADDR", ":8080"),
		OTelEnabled: r.boolOptional("OTEL_ENABLED", false),
		Spotlight:   strings.TrimSpace(getenv("SENTRY_SPOTLIGHT")),

		ChatKillSwitchGlobal:   r.boolOptional("CHAT_KILL_SWITCH", false),
		ChatKillSwitchAccounts: splitList(getenv("CHAT_KILL_SWITCH_ACCOUNTS")),
		LLMServiceBaseURL:      strings.TrimSpace(getenv("LLM_SERVICE_URL")),
		LLMGatewayToken:        strings.TrimSpace(getenv("LLM_GATEWAY_TOKEN")),

		NotifySMTPAddr:          r.optional("SMTP_ADDR", "localhost:1025"),
		NotifyFromAddr:          strings.TrimSpace(getenv("NOTIFY_FROM_ADDR")),
		AppBaseURL:              r.optional("APP_BASE_URL", "http://localhost:5173"),
		NotifyLocale:            r.optional("NOTIFY_LOCALE", "fa-IR"),
		NotifyRegion:            r.optional("NOTIFY_REGION", "IR"),
		CurrencyContractVersion: r.optional("CURRENCY_CONTRACT_VERSION", "v1"),
	}

	if len(r.missing) > 0 {
		return nil, fmt.Errorf("config: missing required environment variables: %s",
			strings.Join(r.missing, ", "))
	}
	return cfg, nil
}

// reader accumulates missing required keys so Load can report them all at once.
type reader struct {
	getenv  func(string) string
	missing []string
}

func (r *reader) required(key string) string {
	v := strings.TrimSpace(r.getenv(key))
	if v == "" {
		r.missing = append(r.missing, key)
	}
	return v
}

func (r *reader) optional(key, def string) string {
	if v := strings.TrimSpace(r.getenv(key)); v != "" {
		return v
	}
	return def
}

// boolOptional treats "1", "true", "yes", "on" (case-insensitive) as true.
func (r *reader) boolOptional(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(r.getenv(key)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
