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
	// once the listener is closed. shutdownDelay + shutdownTimeout must stay
	// below the pod's terminationGracePeriodSeconds.
	shutdownTimeout = 10 * time.Second
)

func mainPage(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintln(w, "Hello, World!")
	if err != nil {
		log.Error().Err(err).Str("path", r.URL.Path).Msg("failed to write response")
		return
	}
}

func livezHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintln(w, "OK")
	if err != nil {
		log.Error().Err(err).Str("path", r.URL.Path).Msg("failed to write response")
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
			log.Error().Err(err).Str("path", r.URL.Path).Msg("failed to write response")
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

func run(ctx context.Context, ln net.Listener, drainDelay time.Duration) error {
	var isReady atomic.Bool
	isReady.Store(true)

	srv := newServer(ln.Addr().String(), setupRouter(&isReady))

	serverErr := make(chan error, 1)
	go func() {
		log.Info().Str("addr", ln.Addr().String()).Msg("server starting")
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
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
			return fmt.Errorf("server error during drain: %w", err)
		case <-time.After(drainDelay):
		}

		log.Info().Str("shutdown_timeout", shutdownTimeout.String()).
			Msg("drain delay elapsed, shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		log.Info().Msg("server gracefully stopped")
		return nil
	}
}

// getListenAddr resolves the server listen address. If the PORT environment
// variable is set, it binds to that port (e.g. "8080" or ":8080").
// If unset or empty, it defaults to ":8080".
func getListenAddr() string {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		return ":8080"
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

	addr := getListenAddr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	return run(ctx, ln, shutdownDelay)
}
