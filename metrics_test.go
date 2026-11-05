package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func scrape(t *testing.T, m *httpMetrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	setupMetricsRouter(m).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /metrics returned %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("GET /metrics Content-Type = %q, want Prometheus text format", ct)
	}
	return rr.Body.String()
}

func TestInstrumentRecordsRED(t *testing.T) {
	var isReady atomic.Bool
	isReady.Store(true)
	m := newHTTPMetrics()
	router := m.instrument(setupRouter(&isReady))

	requests := []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/"},
		{http.MethodHead, "/livez"},
		{http.MethodGet, "/readyz"},
		{http.MethodGet, "/missing"},
		{http.MethodGet, "/also-missing"},
		{http.MethodPost, "/"},
		{"BREW", "/"},
	}
	for _, r := range requests {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(r.method, r.path, nil))
	}
	isReady.Store(false)
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))

	body := scrape(t, m)
	for _, want := range []string{
		"# TYPE http_requests_total counter\n",
		`http_requests_total{method="GET",route="/",code="200"} 2` + "\n",
		// HEAD matches the GET pattern; the route label is the pattern's path.
		`http_requests_total{method="HEAD",route="/livez",code="200"} 1` + "\n",
		`http_requests_total{method="GET",route="/readyz",code="200"} 1` + "\n",
		`http_requests_total{method="GET",route="/readyz",code="503"} 1` + "\n",
		// Unknown paths share one label value instead of one series each.
		`http_requests_total{method="GET",route="unmatched",code="404"} 2` + "\n",
		`http_requests_total{method="POST",route="unmatched",code="405"} 1` + "\n",
		// Non-standard methods are folded into OTHER.
		`http_requests_total{method="OTHER",route="unmatched",code="405"} 1` + "\n",
		"# TYPE http_request_duration_seconds histogram\n",
		`http_request_duration_seconds_bucket{method="GET",route="/",le="+Inf"} 2` + "\n",
		`http_request_duration_seconds_count{method="GET",route="/"} 2` + "\n",
		"# TYPE http_requests_in_flight gauge\n",
		"http_requests_in_flight 0\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n--- got ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "/missing") || strings.Contains(body, "BREW") {
		t.Errorf("metrics output leaks raw request values into labels:\n%s", body)
	}
}

func TestHistogramBuckets(t *testing.T) {
	m := newHTTPMetrics()
	m.observe("GET", "/", 200, 3*time.Millisecond)   // le=0.005
	m.observe("GET", "/", 200, 5*time.Millisecond)   // le=0.005 (bounds are inclusive)
	m.observe("GET", "/", 200, 200*time.Millisecond) // le=0.25
	m.observe("GET", "/", 200, 30*time.Second)       // only +Inf

	body := scrape(t, m)
	for _, want := range []string{
		`http_request_duration_seconds_bucket{method="GET",route="/",le="0.005"} 2` + "\n",
		`http_request_duration_seconds_bucket{method="GET",route="/",le="0.1"} 2` + "\n",
		`http_request_duration_seconds_bucket{method="GET",route="/",le="0.25"} 3` + "\n",
		`http_request_duration_seconds_bucket{method="GET",route="/",le="10"} 3` + "\n",
		`http_request_duration_seconds_bucket{method="GET",route="/",le="+Inf"} 4` + "\n",
		`http_request_duration_seconds_sum{method="GET",route="/"} 30.208` + "\n",
		`http_request_duration_seconds_count{method="GET",route="/"} 4` + "\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n--- got ---\n%s", want, body)
		}
	}
}

func TestInstrumentInFlight(t *testing.T) {
	m := newHTTPMetrics()
	var during string
	handler := m.instrument(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		during = scrape(t, m)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !strings.Contains(during, "http_requests_in_flight 1\n") {
		t.Errorf("in-flight gauge during a request: want 1\n%s", during)
	}
	if after := scrape(t, m); !strings.Contains(after, "http_requests_in_flight 0\n") {
		t.Errorf("in-flight gauge after the request: want 0\n%s", after)
	}
}

func TestStatusWriter(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    int
	}{
		{"implicit 200 on Write", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }, http.StatusOK},
		{"implicit 200 with no body", func(http.ResponseWriter, *http.Request) {}, http.StatusOK},
		{"explicit status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }, http.StatusTeapot},
		{"first WriteHeader wins", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			w.WriteHeader(http.StatusInternalServerError)
		}, http.StatusAccepted},
		{"WriteHeader after Write is ignored", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("x"))
			w.WriteHeader(http.StatusInternalServerError)
		}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sw := &statusWriter{ResponseWriter: httptest.NewRecorder(), code: http.StatusOK}
			tt.handler(sw, httptest.NewRequest(http.MethodGet, "/", nil))
			if sw.code != tt.want {
				t.Errorf("recorded status %d, want %d", sw.code, tt.want)
			}
		})
	}
}

func TestRouteLabel(t *testing.T) {
	tests := map[string]string{
		"":            unmatchedRoute,
		"GET /{$}":    "/",
		"GET /livez":  "/livez",
		"/readyz":     "/readyz",
		"GET /items/": "/items/",
		"/{$}":        "/",
	}
	for pattern, want := range tests {
		if got := routeLabel(pattern); got != want {
			t.Errorf("routeLabel(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestMetricsRouter(t *testing.T) {
	router := setupMetricsRouter(newHTTPMetrics())
	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/metrics", http.StatusOK},
		{http.MethodHead, "/metrics", http.StatusOK},
		{http.MethodPost, "/metrics", http.StatusMethodNotAllowed},
		{http.MethodGet, "/", http.StatusNotFound},
		{http.MethodGet, "/livez", http.StatusNotFound},
	}
	for _, tt := range tests {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(tt.method, tt.path, nil))
		if rr.Code != tt.want {
			t.Errorf("metrics port %s %s returned %d, want %d", tt.method, tt.path, rr.Code, tt.want)
		}
	}
}

func TestMetricsListenAddr(t *testing.T) {
	t.Setenv("METRICS_PORT", "")
	if addr := listenAddr("METRICS_PORT", ":9090"); addr != ":9090" {
		t.Errorf("listenAddr(METRICS_PORT) unset = %q, want :9090", addr)
	}
	t.Setenv("METRICS_PORT", "9191")
	if addr := listenAddr("METRICS_PORT", ":9090"); addr != ":9191" {
		t.Errorf("listenAddr(METRICS_PORT=9191) = %q, want :9191", addr)
	}
}
