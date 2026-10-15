package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
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
		slog.Error("failed to write response", "path", r.URL.Path, "error", err)
		return
	}
}

func livezHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintln(w, "OK")
	if err != nil {
		slog.Error("failed to write response", "path", r.URL.Path, "error", err)
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
			slog.Error("failed to write response", "path", r.URL.Path, "error", err)
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
		// handlers, Accept failures) through slog instead of the std logger.
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}
}

func run(ctx context.Context, ln net.Listener, drainDelay time.Duration) error {
	var isReady atomic.Bool
	isReady.Store(true)

	srv := newServer(ln.Addr().String(), setupRouter(&isReady))

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("server starting", "addr", ln.Addr().String())
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
		slog.Info("shutdown signal received, reporting not-ready and draining",
			"drain_delay", drainDelay.String())

		// Keep accepting connections while endpoint removal propagates;
		// closing the listener immediately would refuse requests that are
		// still being routed to this pod.
		select {
		case err := <-serverErr:
			return fmt.Errorf("server error during drain: %w", err)
		case <-time.After(drainDelay):
		}

		slog.Info("drain delay elapsed, shutting down HTTP server",
			"shutdown_timeout", shutdownTimeout.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		slog.Info("server gracefully stopped")
		return nil
	}
}

func main() {
	// JSON logs on stdout for container log collectors. SetDefault also
	// redirects the std "log" package through this handler.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if err := serve(); err != nil {
		slog.Error("server terminated with error", "error", err)
		os.Exit(1)
	}
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	const addr = ":8080"
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	return run(ctx, ln, shutdownDelay)
}
