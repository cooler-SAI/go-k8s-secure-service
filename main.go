package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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

func mainPage(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintln(w, "Hello, World!")
	if err != nil {
		log.Printf("Error writing response: %v\n", err)
		return
	}
}

func livezHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintln(w, "OK")
	if err != nil {
		log.Printf("Error writing response: %v\n", err)
		return
	}
}

func readyzHandler(isReady *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if isReady != nil && !isReady.Load() {
			http.Error(w, "Not Ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, err := fmt.Fprintln(w, "OK")
		if err != nil {
			log.Printf("Error writing response: %v\n", err)
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
	}
}

func run(ctx context.Context, ln net.Listener, drainDelay time.Duration) error {
	var isReady atomic.Bool
	isReady.Store(true)

	srv := newServer(ln.Addr().String(), setupRouter(&isReady))

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("Server starting on %s...\n", ln.Addr())
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
		log.Printf("Shutdown signal received (SIGTERM/SIGINT), reporting not-ready and draining for %s...\n", drainDelay)

		// Keep accepting connections while endpoint removal propagates;
		// closing the listener immediately would refuse requests that are
		// still being routed to this pod.
		select {
		case err := <-serverErr:
			return fmt.Errorf("server error during drain: %w", err)
		case <-time.After(drainDelay):
		}

		log.Println("Drain delay elapsed, shutting down HTTP server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		log.Println("Server gracefully stopped.")
		return nil
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatalf("Failed to listen on :8080: %v\n", err)
	}

	if err := run(ctx, ln, shutdownDelay); err != nil {
		log.Fatalf("Server terminated with error: %v\n", err)
	}
}
