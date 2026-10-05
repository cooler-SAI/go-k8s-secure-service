package main

import (
	"context"
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

func TestRunGracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Use an ephemeral port on localhost to avoid port collision
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, "127.0.0.1:0")
	}()

	// Allow server goroutine to start
	time.Sleep(50 * time.Millisecond)

	// Trigger shutdown via context cancellation (simulates SIGTERM/SIGINT)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run failed on graceful shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run timed out during graceful shutdown")
	}
}
