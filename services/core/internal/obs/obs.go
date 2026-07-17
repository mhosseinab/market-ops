// Package obs wires process-wide observability: the OpenTelemetry tracer
// provider (behind OTEL_ENABLED) and dev-only Sentry Spotlight delivery (behind
// SENTRY_SPOTLIGHT). Both are off by default and fail closed: when a switch is
// unset no exporter, transport, or global provider is installed, so an outage or
// misconfiguration of the collector/sidecar can never break the service.
//
// This is the S3 collection seam. Span instrumentation of domain code and the
// production dashboards/alerts land in S33 (docs/14); each domain instruments
// its own code against the global provider installed here.
package obs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/mhosseinab/market-ops/services/core/internal/config"
)

// ShutdownFunc flushes and tears down whatever observability was installed. It
// is always non-nil (a no-op when nothing was wired), so callers can defer it
// unconditionally.
type ShutdownFunc func(context.Context) error

// Init installs observability according to cfg and returns a combined shutdown.
// It never installs Sentry unless cfg.SpotlightEnabled(); it never installs an
// OTel exporter unless cfg.OTelEnabled.
func Init(ctx context.Context, cfg *config.Config, logger *slog.Logger) (ShutdownFunc, error) {
	var shutdowns []ShutdownFunc

	if cfg.OTelEnabled {
		sd, err := initTracing(ctx, cfg)
		if err != nil {
			return noop, err
		}
		shutdowns = append(shutdowns, sd)
		logger.Info("otel tracing enabled")
	}

	if cfg.SpotlightEnabled() {
		sd, err := initSpotlight(cfg)
		if err != nil {
			return combine(shutdowns), err
		}
		shutdowns = append(shutdowns, sd)
		logger.Info("sentry spotlight enabled (dev only)", "endpoint", spotlightURL(cfg.Spotlight))
	}

	return combine(shutdowns), nil
}

func initTracing(ctx context.Context, cfg *config.Config) (ShutdownFunc, error) {
	// otlptracehttp reads OTEL_EXPORTER_OTLP_ENDPOINT (and related OTEL_* vars)
	// from the environment; the dev collector is provided by compose.dev.yml.
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", config.ServiceName),
			attribute.String("deployment.environment", cfg.AppEnv),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

func initSpotlight(cfg *config.Config) (ShutdownFunc, error) {
	// No DSN: with an empty DSN sentry-go installs a no-op transport, so we
	// supply an explicit transport that delivers envelopes to the local
	// Spotlight sidecar instead. Nothing is ever sent to Sentry's cloud.
	transport := newSpotlightTransport(spotlightURL(cfg.Spotlight))

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              "",
		Environment:      cfg.AppEnv,
		EnableTracing:    true,
		TracesSampleRate: 1.0,
		Transport:        transport,
	})
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context) error {
		deadline := 2 * time.Second
		if d, ok := ctx.Deadline(); ok {
			if remaining := time.Until(d); remaining < deadline {
				deadline = remaining
			}
		}
		sentry.Flush(deadline)
		return nil
	}, nil
}

// noop is the zero-observability shutdown.
func noop(context.Context) error { return nil }

func combine(sds []ShutdownFunc) ShutdownFunc {
	if len(sds) == 0 {
		return noop
	}
	return func(ctx context.Context) error {
		var errs []error
		// Tear down in reverse installation order.
		for i := len(sds) - 1; i >= 0; i-- {
			if err := sds[i](ctx); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
}
