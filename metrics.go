package main

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// RED metrics (Rate, Errors, Duration) in the Prometheus text exposition
// format, implemented on the standard library so the service keeps a single
// direct dependency:
//
//   - http_requests_total{method,route,code}: request rate, and error rate
//     via code=~"5..".
//   - http_request_duration_seconds{method,route}: latency histogram.
//   - http_requests_in_flight: requests currently being served.

// defaultBuckets are Prometheus' default histogram buckets, in seconds.
var defaultBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// unmatchedRoute labels requests that matched no route (404, 405). Using the
// raw path instead would let any client create unbounded label values.
const unmatchedRoute = "unmatched"

type requestKey struct{ method, route, code string }

type durationKey struct{ method, route string }

type histogram struct {
	counts []uint64 // per bucket, not cumulative; len(buckets)+1 for +Inf
	sum    float64
	count  uint64
}

type httpMetrics struct {
	buckets  []float64
	inFlight atomic.Int64

	mu        sync.Mutex
	requests  map[requestKey]uint64
	durations map[durationKey]*histogram
}

func newHTTPMetrics() *httpMetrics {
	return &httpMetrics{
		buckets:   defaultBuckets,
		requests:  make(map[requestKey]uint64),
		durations: make(map[durationKey]*histogram),
	}
}

func (m *httpMetrics) observe(method, route string, code int, d time.Duration) {
	seconds := d.Seconds()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests[requestKey{method, route, strconv.Itoa(code)}]++

	dk := durationKey{method, route}
	h := m.durations[dk]
	if h == nil {
		h = &histogram{counts: make([]uint64, len(m.buckets)+1)}
		m.durations[dk] = h
	}
	i, _ := slices.BinarySearch(m.buckets, seconds) // first bucket with le >= seconds
	h.counts[i]++
	h.sum += seconds
	h.count++
}

// instrument records RED metrics for every request served by next, which
// must be (or wrap) the *http.ServeMux whose matched pattern labels the route.
func (m *httpMetrics) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inFlight.Add(1)
		defer m.inFlight.Add(-1)

		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		// ServeMux stores the matched pattern on r before calling the handler.
		m.observe(methodLabel(r.Method), routeLabel(r.Pattern), sw.code, time.Since(start))
	})
}

// methodLabel maps the request method to a bounded set of label values.
func methodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect,
		http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

// routeLabel turns a ServeMux pattern such as "GET /{$}" into "/".
func routeLabel(pattern string) string {
	if pattern == "" {
		return unmatchedRoute
	}
	if _, path, ok := strings.Cut(pattern, " "); ok {
		pattern = path
	}
	if route := strings.TrimSuffix(pattern, "{$}"); route != "" {
		return route
	}
	return pattern
}

// statusWriter captures the status code written by the wrapped handler.
type statusWriter struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.code = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// ServeHTTP writes all metrics in the Prometheus text exposition format.
func (m *httpMetrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	m.writeText(&b)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if _, err := io.WriteString(w, b.String()); err != nil {
		log.Error().Err(err).Str("path", r.URL.Path).Msg("failed to write response")
	}
}

func (m *httpMetrics) writeText(b *strings.Builder) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b.WriteString("# HELP http_requests_total Total HTTP requests by method, route and status code.\n")
	b.WriteString("# TYPE http_requests_total counter\n")
	reqKeys := slices.SortedFunc(maps.Keys(m.requests), func(a, b requestKey) int {
		return strings.Compare(a.method+" "+a.route+" "+a.code, b.method+" "+b.route+" "+b.code)
	})
	for _, k := range reqKeys {
		fmt.Fprintf(b, "http_requests_total{method=%q,route=%q,code=%q} %d\n",
			escapeLabel(k.method), escapeLabel(k.route), escapeLabel(k.code), m.requests[k])
	}

	b.WriteString("# HELP http_request_duration_seconds HTTP request latency by method and route.\n")
	b.WriteString("# TYPE http_request_duration_seconds histogram\n")
	durKeys := slices.SortedFunc(maps.Keys(m.durations), func(a, b durationKey) int {
		return strings.Compare(a.method+" "+a.route, b.method+" "+b.route)
	})
	for _, k := range durKeys {
		h := m.durations[k]
		labels := fmt.Sprintf("method=%q,route=%q", escapeLabel(k.method), escapeLabel(k.route))
		var cumulative uint64
		for i, le := range m.buckets {
			cumulative += h.counts[i]
			fmt.Fprintf(b, "http_request_duration_seconds_bucket{%s,le=%q} %d\n",
				labels, strconv.FormatFloat(le, 'g', -1, 64), cumulative)
		}
		fmt.Fprintf(b, "http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, h.count)
		fmt.Fprintf(b, "http_request_duration_seconds_sum{%s} %s\n", labels, strconv.FormatFloat(h.sum, 'g', -1, 64))
		fmt.Fprintf(b, "http_request_duration_seconds_count{%s} %d\n", labels, h.count)
	}

	b.WriteString("# HELP http_requests_in_flight Requests currently being served.\n")
	b.WriteString("# TYPE http_requests_in_flight gauge\n")
	fmt.Fprintf(b, "http_requests_in_flight %d\n", m.inFlight.Load())
}

// escapeLabel is a no-op for the bounded label values used here; it only
// keeps %q from emitting Go escapes that the exposition format lacks.
func escapeLabel(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return '_'
		}
		return r
	}, s)
}
