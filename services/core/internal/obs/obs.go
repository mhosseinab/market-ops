// Package obs wires process-wide observability: the OpenTelemetry tracer
// provider (behind OTEL_ENABLED) and dev-only Sentry Spotlight delivery (behind
// SENTRY_SPOTLIGHT). Both are off by default and fail closed: when a switch is
// unset no exporter, transport, or global provider is installed, so an outage or
// misconfiguration of the collector/sidecar can never break the service.
//
// This is the S3 collection seam, completed in S33: the tracer AND meter global
// providers are installed here (behind OTEL_ENABLED), plus the W3C trace-context
// propagator, so the S18 execution telemetry and S19 analytics/cost metric seams
// actually export and an inbound web → gateway trace continues into core spans.
// Each domain instruments its own code against these global providers; the §18
// dashboards and §20.1 alerts consume the exported series (docs/14).
package obs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
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
		// A single W3C trace-context + baggage propagator so an incoming trace
		// (web → gateway) continues into core spans and outbound calls (core → DK,
		// core → LLM plane). Set unconditionally with tracing so the approval-control
		// identity (action ID + parameter/context version) survives every hop.
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{}, propagation.Baggage{},
		))

		sd, err := initTracing(ctx, cfg)
		if err != nil {
			return noop, err
		}
		shutdowns = append(shutdowns, sd)
		logger.Info("otel tracing enabled")

		msd, err := initMetrics(ctx, cfg)
		if err != nil {
			return combine(shutdowns), err
		}
		shutdowns = append(shutdowns, msd)
		logger.Info("otel metrics enabled")
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

// initMetrics installs the global MeterProvider that exports the domain metric
// seams wired by S18 (execution) and S19 (analytics/cost) plus the RED/latency
// instruments added in S33. Without it those meters resolve to the global no-op
// and nothing reaches the collector, so the §18 dashboards would be empty of
// real series. otlpmetrichttp reads the same OTEL_EXPORTER_OTLP_* env the tracer
// uses; the dev collector is provided by compose.dev.yml. The resource carries
// service.name so Prometheus can roll metrics up by service/version.
func initMetrics(ctx context.Context, cfg *config.Config) (ShutdownFunc, error) {
	exporter, err := otlpmetrichttp.New(ctx)
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

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return mp.Shutdown, nil
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
