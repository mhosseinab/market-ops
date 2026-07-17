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
}

// SpotlightEnabled reports whether dev Spotlight wiring should be initialized.
// Unset SENTRY_SPOTLIGHT ⇒ false ⇒ Sentry is fully disabled.
func (c Config) SpotlightEnabled() bool { return c.Spotlight != "" }

// Load reads configuration using getenv (pass os.Getenv in production; a fake in
// tests). It returns an error listing every missing required variable.
func Load(getenv func(string) string) (*Config, error) {
	r := reader{getenv: getenv}

	cfg := &Config{
		AppEnv:      r.required("APP_ENV"),
		HTTPAddr:    r.optional("HTTP_ADDR", ":8080"),
		OTelEnabled: r.boolOptional("OTEL_ENABLED", false),
		Spotlight:   strings.TrimSpace(getenv("SENTRY_SPOTLIGHT")),
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
