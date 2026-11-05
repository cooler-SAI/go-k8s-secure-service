package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// OpenTelemetry tracing with W3C Trace Context propagation. Incoming requests
// continue the caller's trace from the traceparent header (or start a new
// one); outgoing requests made with newOutboundClient carry it on. Spans are
// exported over OTLP/HTTP only when an OTLP endpoint is configured through the
// standard OTEL_* environment variables.

const (
	defaultServiceName = "go-k8s-secure-service"

	// tracingShutdownTimeout bounds the final span flush on shutdown. Together
	// with shutdownDelay and shutdownTimeout it must stay below the pod's
	// terminationGracePeriodSeconds.
	tracingShutdownTimeout = 5 * time.Second
)

type tracing struct {
	provider   trace.TracerProvider
	propagator propagation.TextMapPropagator
	// exporting reports whether spans are sent to an OTLP endpoint.
	exporting bool
	shutdown  func(context.Context) error
}

// newPropagator handles the W3C traceparent/tracestate and baggage headers.
func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

// newTracing configures tracing from the standard OpenTelemetry environment
// variables and installs it as the global provider and propagator:
//
//   - OTEL_SDK_DISABLED=true: no spans are recorded, but traceparent is still
//     passed through from incoming to outgoing requests.
//   - OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:
//     spans are batched and exported over OTLP/HTTP. Unset: spans are
//     created (trace IDs appear in logs) but not exported.
//   - OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES, OTEL_TRACES_SAMPLER(_ARG)
//     and the other OTEL_EXPORTER_OTLP_* variables are read by the SDK.
func newTracing(ctx context.Context) (*tracing, error) {
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn().Err(err).Str("component", "otel").Msg("opentelemetry error")
	}))

	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true") {
		tp := tracenoop.NewTracerProvider()
		otel.SetTracerProvider(tp)
		return &tracing{provider: tp, propagator: prop, shutdown: func(context.Context) error { return nil }}, nil
	}

	// Later options override earlier ones, so OTEL_SERVICE_NAME and
	// OTEL_RESOURCE_ATTRIBUTES take precedence over the default name.
	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(semconv.ServiceName(defaultServiceName)),
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("building tracing resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	exporting := otlpEndpointConfigured()
	if exporting {
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("creating OTLP trace exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return &tracing{provider: tp, propagator: prop, exporting: exporting, shutdown: tp.Shutdown}, nil
}

func otlpEndpointConfigured() bool {
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != ""
}

// handler wraps next, which must be (or pass its request unchanged to) the
// *http.ServeMux, with a server span per request. It extracts traceparent
// from the request, so the span joins the caller's trace.
func (t *tracing) handler(next http.Handler) http.Handler {
	return otelhttp.NewHandler(annotateRoute(next), "http.server",
		otelhttp.WithTracerProvider(t.provider),
		otelhttp.WithPropagators(t.propagator),
		// HTTP metrics come from metrics.go; don't record a second set.
		otelhttp.WithMeterProvider(metricnoop.NewMeterProvider()),
		otelhttp.WithFilter(isTraced),
		otelhttp.WithSpanNameFormatter(spanName),
	)
}

// isTraced skips the kubelet probes, which would otherwise produce a span
// every few seconds per pod without telling anything about user traffic.
func isTraced(r *http.Request) bool {
	return r.URL.Path != "/livez" && r.URL.Path != "/readyz"
}

// spanName follows the HTTP semantic conventions: "{method} {route}", or just
// "{method}" when no route matched. otelhttp calls it again after the mux has
// set r.Pattern.
func spanName(_ string, r *http.Request) string {
	if r.Pattern == "" {
		return methodLabel(r.Method)
	}
	return methodLabel(r.Method) + " " + routeLabel(r.Pattern)
}

// annotateRoute adds the matched route as http.route once the mux has run;
// at span start the pattern is not known yet.
func annotateRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Pattern != "" {
			trace.SpanFromContext(r.Context()).SetAttributes(semconv.HTTPRoute(routeLabel(r.Pattern)))
		}
	})
}

// newOutboundClient returns the HTTP client to use for outgoing calls. Its
// transport starts a client span and injects traceparent (and baggage) from
// the request context, so the downstream service continues the trace. Build
// requests with the incoming request's context, e.g.
// http.NewRequestWithContext(r.Context(), ...).
func newOutboundClient(t *tracing) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport,
			otelhttp.WithTracerProvider(t.provider),
			otelhttp.WithPropagators(t.propagator),
			otelhttp.WithMeterProvider(metricnoop.NewMeterProvider()),
		),
	}
}

// requestLogger returns the global logger with the request's trace_id and
// span_id, so log lines can be joined with their trace.
func requestLogger(r *http.Request) *zerolog.Logger {
	l := log.Logger
	if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
		l = l.With().Str("trace_id", sc.TraceID().String()).Str("span_id", sc.SpanID().String()).Logger()
	}
	return &l
}
