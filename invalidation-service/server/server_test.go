package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hossainshakhawat/distributed-cache/invalidation-service/queue"
)

func newTestInvalidationServer() (*Server, *queue.Bus) {
	b := queue.NewBus()
	return New(b), b
}

func TestHandleInvalidate_Success(t *testing.T) {
	srv, bus := newTestInvalidationServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	ch := bus.Subscribe(10)

	body, _ := json.Marshal(invalidateRequest{Keys: []string{"k1", "k2"}, Source: "svc"})
	req := httptest.NewRequest(http.MethodPost, "/invalidate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}
	select {
	case ev := <-ch:
		if len(ev.Keys) != 2 || ev.Keys[0] != "k1" || ev.Source != "svc" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for event on bus")
	}
}

func TestHandleInvalidate_BadMethod(t *testing.T) {
	srv, _ := newTestInvalidationServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/invalidate", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHandleInvalidate_EmptyKeys(t *testing.T) {
	srv, _ := newTestInvalidationServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body, _ := json.Marshal(invalidateRequest{Keys: nil, Source: "svc"})
	req := httptest.NewRequest(http.MethodPost, "/invalidate", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleInvalidate_BadBody(t *testing.T) {
	srv, _ := newTestInvalidationServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/invalidate", bytes.NewReader([]byte("not-json")))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	srv, _ := newTestInvalidationServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %q", resp["status"])
	}
}
