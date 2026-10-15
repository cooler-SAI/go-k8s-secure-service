package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainPage(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "root path returns greeting",
			path:           "/",
			expectedStatus: http.StatusOK,
			expectedBody:   "Hello, World!\n",
		},
		{
			name:           "non-root path returns 404",
			path:           "/unknown",
			expectedStatus: http.StatusNotFound,
			expectedBody:   "404 page not found\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rr := httptest.NewRecorder()

			mainPage(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("mainPage returned wrong status code: got %v want %v", status, tt.expectedStatus)
			}

			if body := rr.Body.String(); body != tt.expectedBody {
				t.Errorf("mainPage returned unexpected body: got %q want %q", body, tt.expectedBody)
			}
		})
	}
}

func TestLivezHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rr := httptest.NewRecorder()

	livezHandler(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("livezHandler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "OK\n"
	if body := rr.Body.String(); body != expected {
		t.Errorf("livezHandler returned unexpected body: got %q want %q", body, expected)
	}
}

func TestReadyzHandler(t *testing.T) {
	tests := []struct {
		name           string
		readyVal       *bool
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "ready when atomic flag is true",
			readyVal:       boolPtr(true),
			expectedStatus: http.StatusOK,
			expectedBody:   "OK\n",
		},
		{
			name:           "not ready when atomic flag is false",
			readyVal:       boolPtr(false),
			expectedStatus: http.StatusServiceUnavailable,
			expectedBody:   "Not Ready\n",
		},
		{
			name:           "ready when atomic pointer is nil",
			readyVal:       nil,
			expectedStatus: http.StatusOK,
			expectedBody:   "OK\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var isReady *atomic.Bool
			if tt.readyVal != nil {
				isReady = &atomic.Bool{}
				isReady.Store(*tt.readyVal)
			}

			handler := readyzHandler(isReady)
			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			rr := httptest.NewRecorder()

			handler(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("readyzHandler returned status %v, want %v", status, tt.expectedStatus)
			}
			if body := rr.Body.String(); body != tt.expectedBody {
				t.Errorf("readyzHandler returned body %q, want %q", body, tt.expectedBody)
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func TestRouter(t *testing.T) {
	var isReady atomic.Bool
	isReady.Store(true)
	router := setupRouter(&isReady)

	tests := []struct {
		name           string
		path           string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "route /",
			path:           "/",
			expectedStatus: http.StatusOK,
			expectedBody:   "Hello, World!\n",
		},
		{
			name:           "route /livez",
			path:           "/livez",
			expectedStatus: http.StatusOK,
			expectedBody:   "OK\n",
		},
		{
			name:           "route /readyz when ready",
			path:           "/readyz",
			expectedStatus: http.StatusOK,
			expectedBody:   "OK\n",
		},
		{
			name:           "route unknown",
			path:           "/random",
			expectedStatus: http.StatusNotFound,
			expectedBody:   "404 page not found\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rr := httptest.NewRecorder()

			router.ServeHTTP(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("%s returned status %d, want %d", tt.path, rr.Code, tt.expectedStatus)
			}
			if !strings.Contains(rr.Body.String(), tt.expectedBody) {
				t.Errorf("%s returned body %q, want %q", tt.path, rr.Body.String(), tt.expectedBody)
			}
		})
	}

	// Also verify /readyz switches to 503 when not ready
	isReady.Store(false)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz when not ready returned %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestNewServer(t *testing.T) {
	var isReady atomic.Bool
	isReady.Store(true)
	router := setupRouter(&isReady)
	srv := newServer(":8080", router)

	if srv.Addr != ":8080" {
		t.Errorf("expected Addr :8080, got %s", srv.Addr)
	}
	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("expected ReadHeaderTimeout 5s, got %v", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 10*time.Second {
		t.Errorf("expected ReadTimeout 10s, got %v", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 10*time.Second {
		t.Errorf("expected WriteTimeout 10s, got %v", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 60*time.Second {
		t.Errorf("expected IdleTimeout 60s, got %v", srv.IdleTimeout)
	}
}

func newTestListener(t *testing.T) net.Listener {
	t.Helper()
	// Use an ephemeral port on localhost to avoid port collision
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	return ln
}

func getStatus(client *http.Client, url string) (int, error) {
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func TestRunGracefulShutdown(t *testing.T) {
	const drainDelay = 300 * time.Millisecond

	ln := newTestListener(t)
	readyzURL := "http://" + ln.Addr().String() + "/readyz"
	client := &http.Client{
		Timeout:   time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- run(ctx, ln, drainDelay)
	}()

	if status, err := getStatus(client, readyzURL); err != nil || status != http.StatusOK {
		t.Fatalf("/readyz before shutdown: status %d, err %v; want 200", status, err)
	}

	// Trigger shutdown via context cancellation (simulates SIGTERM/SIGINT)
	shutdownStart := time.Now()
	cancel()

	// During the drain window the server must keep accepting connections
	// while reporting not-ready, so in-flight routing does not hit a closed port.
	deadline := time.Now().Add(drainDelay / 2)
	for {
		status, err := getStatus(client, readyzURL)
		if err != nil {
			t.Fatalf("/readyz during drain window: connection failed: %v", err)
		}
		if status == http.StatusServiceUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/readyz during drain window: status %d, want 503", status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run failed on graceful shutdown: %v", err)
		}
		if elapsed := time.Since(shutdownStart); elapsed < drainDelay {
			t.Errorf("run returned after %v, before drain delay %v elapsed", elapsed, drainDelay)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run timed out during graceful shutdown")
	}

	if _, err := getStatus(client, readyzURL); err == nil {
		t.Error("server still accepting connections after shutdown")
	}
}

func TestRunServeError(t *testing.T) {
	ln := newTestListener(t)
	// A closed listener makes Serve fail immediately.
	_ = ln.Close()

	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), ln, 0)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("run returned nil, want server error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return after serve error")
	}
}
