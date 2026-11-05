package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	// A W3C traceparent as an upstream service (or ingress) would send it.
	incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	incomingSpanID  = "00f067aa0ba902b7"
	incomingParent  = "00-" + incomingTraceID + "-" + incomingSpanID + "-01"
)

// newRecordingTracing returns tracing backed by an SDK provider that keeps
// finished spans in memory instead of exporting them.
func newRecordingTracing(t *testing.T) (*tracing, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return &tracing{provider: tp, propagator: newPropagator(), shutdown: tp.Shutdown}, sr
}

func newTestTracing(t *testing.T) *tracing {
	tr, _ := newRecordingTracing(t)
	return tr
}

func spanAttr(s sdktrace.ReadOnlySpan, key attribute.Key) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// TestTracingPropagatesTraceparent sends a request carrying traceparent to a
// handler that makes an outgoing call, and checks that the trace continues
// across both hops: incoming traceparent -> server span -> client span ->
// traceparent received downstream.
func TestTracingPropagatesTraceparent(t *testing.T) {
	tr, sr := newRecordingTracing(t)

	var downstreamParent atomic.Value
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamParent.Store(r.Header.Get("traceparent"))
	}))
	defer downstream.Close()

	client := newOutboundClient(tr)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /call", func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, downstream.URL, nil)
		if err != nil {
			t.Errorf("building outgoing request: %v", err)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("outgoing request: %v", err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	})
	frontend := httptest.NewServer(tr.handler(newHTTPMetrics().instrument(mux)))
	defer frontend.Close()

	req, _ := http.NewRequest(http.MethodGet, frontend.URL+"/call", nil)
	req.Header.Set("traceparent", incomingParent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("incoming request: %v", err)
	}
	_ = resp.Body.Close()

	spans := sr.Ended()
	if len(spans) != 2 {
		t.Fatalf("recorded %d spans, want 2 (server and client)", len(spans))
	}
	var server, client2 sdktrace.ReadOnlySpan
	for _, s := range spans {
		switch s.SpanKind() {
		case trace.SpanKindServer:
			server = s
		case trace.SpanKindClient:
			client2 = s
		}
	}
	if server == nil || client2 == nil {
		t.Fatalf("want one server and one client span, got kinds %v and %v", spans[0].SpanKind(), spans[1].SpanKind())
	}

	// The server span continues the caller's trace.
	if got := server.SpanContext().TraceID().String(); got != incomingTraceID {
		t.Errorf("server span trace ID = %s, want %s", got, incomingTraceID)
	}
	if got := server.Parent().SpanID().String(); got != incomingSpanID || !server.Parent().IsRemote() {
		t.Errorf("server span parent = %s (remote %v), want remote %s", got, server.Parent().IsRemote(), incomingSpanID)
	}
	if server.Name() != "GET /call" {
		t.Errorf("server span name = %q, want %q", server.Name(), "GET /call")
	}
	if v, ok := spanAttr(server, semconv.HTTPRouteKey); !ok || v.AsString() != "/call" {
		t.Errorf("server span http.route = %q (set %v), want /call", v.AsString(), ok)
	}

	// The client span is a child of the server span in the same trace.
	if client2.Parent().SpanID() != server.SpanContext().SpanID() {
		t.Errorf("client span parent = %s, want server span %s", client2.Parent().SpanID(), server.SpanContext().SpanID())
	}

	// Downstream receives a traceparent naming the client span as parent.
	want := "00-" + incomingTraceID + "-" + client2.SpanContext().SpanID().String() + "-01"
	if got, _ := downstreamParent.Load().(string); got != want {
		t.Errorf("downstream traceparent = %q, want %q", got, want)
	}
}

func TestTracingStartsNewTraceWithoutTraceparent(t *testing.T) {
	tr, sr := newRecordingTracing(t)
	var isReady atomic.Bool
	isReady.Store(true)
	handler := tr.handler(newHTTPMetrics().instrument(setupRouter(&isReady)))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	s := spans[0]
	if !s.SpanContext().TraceID().IsValid() || s.Parent().IsValid() {
		t.Errorf("want a new root span, got trace %s with parent %s", s.SpanContext().TraceID(), s.Parent().SpanID())
	}
	if s.Name() != "GET /" {
		t.Errorf("span name = %q, want %q", s.Name(), "GET /")
	}
}

func TestTracingSpanNames(t *testing.T) {
	tr, sr := newRecordingTracing(t)
	var isReady atomic.Bool
	isReady.Store(true)
	handler := tr.handler(newHTTPMetrics().instrument(setupRouter(&isReady)))

	for _, r := range []struct{ method, path string }{
		{http.MethodHead, "/"},
		{http.MethodGet, "/missing"},
		{http.MethodPost, "/"},
		{"BREW", "/"},
	} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(r.method, r.path, nil))
	}

	var names []string
	for _, s := range sr.Ended() {
		names = append(names, s.Name())
	}
	// Unmatched requests are named by method only, so raw paths never become
	// span names; unknown methods are folded like the metrics labels.
	want := []string{"HEAD /", "GET", "POST", "OTHER"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("span names = %v, want %v", names, want)
	}
}

func TestTracingSkipsProbes(t *testing.T) {
	tr, sr := newRecordingTracing(t)
	var isReady atomic.Bool
	isReady.Store(true)
	handler := tr.handler(newHTTPMetrics().instrument(setupRouter(&isReady)))

	for _, path := range []string{"/livez", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("traceparent", incomingParent)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s returned %d, want 200", path, rr.Code)
		}
	}
	if n := len(sr.Ended()); n != 0 {
		t.Errorf("probes recorded %d spans, want 0", n)
	}
}

// Metrics must keep their route labels when wrapped by tracing.
func TestTracingKeepsMetricRoutes(t *testing.T) {
	tr := newTestTracing(t)
	m := newHTTPMetrics()
	var isReady atomic.Bool
	isReady.Store(true)
	handler := tr.handler(m.instrument(setupRouter(&isReady)))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if body := scrape(t, m); !strings.Contains(body, `http_requests_total{method="GET",route="/",code="200"} 1`) {
		t.Errorf("route label lost under tracing:\n%s", body)
	}
}

func TestRequestLogger(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = orig })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	requestLogger(req).Info().Msg("no span")

	tr, _ := newRecordingTracing(t)
	ctx, span := tr.provider.Tracer("test").Start(context.Background(), "op")
	defer span.End()
	requestLogger(req.WithContext(ctx)).Info().Msg("with span")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2: %q", len(lines), buf.String())
	}
	var noSpan, withSpan map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &noSpan)
	_ = json.Unmarshal([]byte(lines[1]), &withSpan)

	if _, ok := noSpan["trace_id"]; ok {
		t.Errorf("log without span has trace_id: %s", lines[0])
	}
	sc := span.SpanContext()
	if withSpan["trace_id"] != sc.TraceID().String() || withSpan["span_id"] != sc.SpanID().String() {
		t.Errorf("log with span = %s, want trace_id %s and span_id %s", lines[1], sc.TraceID(), sc.SpanID())
	}
}

func TestNewTracing(t *testing.T) {
	clearEnv := func(t *testing.T) {
		for _, k := range []string{"OTEL_SDK_DISABLED", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"} {
			t.Setenv(k, "")
		}
	}

	t.Run("no endpoint: spans recorded, not exported", func(t *testing.T) {
		clearEnv(t)
		tr, err := newTracing(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer tr.shutdown(context.Background())
		if tr.exporting {
			t.Error("exporting = true without an OTLP endpoint")
		}
		if _, ok := tr.provider.(*sdktrace.TracerProvider); !ok {
			t.Errorf("provider = %T, want SDK provider", tr.provider)
		}
	})

	t.Run("endpoint set: spans exported over OTLP/HTTP", func(t *testing.T) {
		clearEnv(t)
		var exports atomic.Int32
		collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/v1/traces" &&
				r.Header.Get("Content-Type") == "application/x-protobuf" {
				exports.Add(1)
			}
			w.Header().Set("Content-Type", "application/x-protobuf")
		}))
		defer collector.Close()
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)

		tr, err := newTracing(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !tr.exporting {
			t.Error("exporting = false with OTEL_EXPORTER_OTLP_ENDPOINT set")
		}
		tr.handler(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		// Shutdown flushes the batch, like serve() does on exit.
		if err := tr.shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if exports.Load() == 0 {
			t.Error("no OTLP export request reached the collector")
		}
	})

	t.Run("SDK disabled: traceparent still passed through", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("OTEL_SDK_DISABLED", "true")
		tr, err := newTracing(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := tr.provider.(*sdktrace.TracerProvider); ok {
			t.Error("SDK provider installed although OTEL_SDK_DISABLED=true")
		}

		var got string
		downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("traceparent")
		}))
		defer downstream.Close()
		handler := tr.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, downstream.URL, nil)
			if resp, err := newOutboundClient(tr).Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("traceparent", incomingParent)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if got != incomingParent {
			t.Errorf("downstream traceparent = %q, want unchanged %q", got, incomingParent)
		}
	})
}
