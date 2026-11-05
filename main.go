package main

import (
	"context"
	"errors"
	"fmt"
	stdlog "log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	// shutdownDelay is how long the server keeps serving after a termination
	// signal while reporting not-ready. Kubernetes removes a terminating pod
	// from Service endpoints asynchronously, so kube-proxy and ingress
	// controllers may keep routing new requests to it for a few seconds.
	shutdownDelay = 5 * time.Second

	// shutdownTimeout bounds how long in-flight requests may take to finish
	// once the listener is closed. shutdownDelay + shutdownTimeout +
	// tracingShutdownTimeout must stay below the pod's
	// terminationGracePeriodSeconds.
	shutdownTimeout = 10 * time.Second
)

func mainPage(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintln(w, "Hello, World!")
	if err != nil {
		requestLogger(r).Error().Err(err).Str("path", r.URL.Path).Msg("failed to write response")
		return
	}
}

func livezHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintln(w, "OK")
	if err != nil {
		requestLogger(r).Error().Err(err).Str("path", r.URL.Path).Msg("failed to write response")
		return
	}
}

func readyzHandler(isReady *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isReady != nil && !isReady.Load() {
			http.Error(w, "Not Ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, err := fmt.Fprintln(w, "OK")
		if err != nil {
			requestLogger(r).Error().Err(err).Str("path", r.URL.Path).Msg("failed to write response")
			return
		}
	}
}

func setupRouter(isReady *atomic.Bool) http.Handler {
	// Method-qualified patterns: GET also matches HEAD, other methods get
	// 405 Method Not Allowed. "/{$}" matches only the exact root path, so
	// unknown paths fall through to the mux's 404.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", mainPage)
	mux.HandleFunc("GET /livez", livezHandler)
	mux.HandleFunc("GET /readyz", readyzHandler(isReady))
	return mux
}

// setupMetricsRouter serves /metrics on the internal metrics port only, so
// it is never reachable through the traffic Service or an Ingress.
func setupMetricsRouter(metrics *httpMetrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics)
	return mux
}

func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		// Route net/http's internal errors (TLS handshakes, panics in
		// handlers, Accept failures) through zerolog instead of the std logger.
		ErrorLog: stdlog.New(log.Logger.With().Str("component", "net/http").Logger(), "", 0),
	}
}

// run serves application traffic on ln and Prometheus metrics on metricsLn
// until ctx is cancelled, then drains and shuts both servers down. Traffic
// requests are traced with tr.
func run(ctx context.Context, ln, metricsLn net.Listener, drainDelay time.Duration, tr *tracing) error {
	var isReady atomic.Bool
	isReady.Store(true)

	metrics := newHTTPMetrics()
	// Tracing must wrap the metrics middleware, not the other way round:
	// otelhttp passes a copy of the request down, and both rely on the mux
	// setting the matched pattern on the request they hold.
	srv := newServer(ln.Addr().String(), tr.handler(metrics.instrument(setupRouter(&isReady))))
	metricsSrv := newServer(metricsLn.Addr().String(), setupMetricsRouter(metrics))

	// Buffered for both servers, so neither goroutine blocks on send.
	serverErr := make(chan error, 2)
	serve := func(name string, s *http.Server, l net.Listener) {
		log.Info().Str("server", name).Str("addr", l.Addr().String()).Msg("server starting")
		if err := s.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("%s server: %w", name, err)
		}
	}
	go serve("http", srv, ln)
	go serve("metrics", metricsSrv, metricsLn)

	// If either server fails, stop the other one too.
	closeAll := func() {
		_ = srv.Close()
		_ = metricsSrv.Close()
	}

	select {
	case err := <-serverErr:
		closeAll()
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		isReady.Store(false)
		log.Info().Str("drain_delay", drainDelay.String()).
			Msg("shutdown signal received, reporting not-ready and draining")

		// Keep accepting connections while endpoint removal propagates;
		// closing the listener immediately would refuse requests that are
		// still being routed to this pod.
		select {
		case err := <-serverErr:
			closeAll()
			return fmt.Errorf("server error during drain: %w", err)
		case <-time.After(drainDelay):
		}

		log.Info().Str("shutdown_timeout", shutdownTimeout.String()).
			Msg("drain delay elapsed, shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		// Traffic first, then metrics, so the drain stays observable until
		// the last in-flight request has finished.
		if err := errors.Join(srv.Shutdown(shutdownCtx), metricsSrv.Shutdown(shutdownCtx)); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		log.Info().Msg("server gracefully stopped")
		return nil
	}
}

// listenAddr resolves a listen address from the environment variable env
// (e.g. PORT=8080 or PORT=:8080). If unset or empty, it returns fallback.
func listenAddr(env, fallback string) string {
	port := strings.TrimSpace(os.Getenv(env))
	if port == "" {
		return fallback
	}
	if !strings.HasPrefix(port, ":") {
		return ":" + port
	}
	return port
}

func main() {
	// JSON logs on stdout for container log collectors. The global zerolog
	// logger is also installed as the std "log" package's output.
	zerolog.TimeFieldFormat = time.RFC3339
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	stdlog.SetFlags(0)
	stdlog.SetOutput(log.Logger)

	if err := serve(); err != nil {
		log.Error().Err(err).Msg("server terminated with error")
		os.Exit(1)
	}
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := listenAddr("PORT", ":8080")
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	metricsAddr := listenAddr("METRICS_PORT", ":9090")
	metricsLn, err := net.Listen("tcp", metricsAddr)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("failed to listen on %s for metrics: %w", metricsAddr, err)
	}

	tr, err := newTracing(ctx)
	if err != nil {
		_ = ln.Close()
		_ = metricsLn.Close()
		return fmt.Errorf("failed to set up tracing: %w", err)
	}
	log.Info().Bool("exporting", tr.exporting).Msg("tracing configured")

	runErr := run(ctx, ln, metricsLn, shutdownDelay, tr)

	// Flush buffered spans after the servers have stopped, so the spans of
	// the last drained requests are exported too.
	flushCtx, cancel := context.WithTimeout(context.Background(), tracingShutdownTimeout)
	defer cancel()
	if err := tr.shutdown(flushCtx); err != nil {
		log.Warn().Err(err).Msg("tracing shutdown failed, some spans may be lost")
	}
	return runErr
}
