package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hossainshakhawat/distributed-cache/cache-node/store"
)

func newTestServer() *Server {
	return New(store.New(100, store.PolicyLRU), nil)
}

func TestHandleHealth(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandleGet_Miss(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/get?key=missing", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp GetResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Hit {
		t.Fatal("expected miss")
	}
	if resp.Key != "missing" {
		t.Fatalf("want key='missing', got %q", resp.Key)
	}
}

func TestHandleGet_Hit(t *testing.T) {
	srv := newTestServer()
	srv.store.Set("k", []byte("hello"), time.Minute, 42)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/get?key=k", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var resp GetResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Hit {
		t.Fatal("expected hit")
	}
	if string(resp.Value) != "hello" {
		t.Fatalf("want value 'hello', got %q", resp.Value)
	}
	if resp.Version != 42 {
		t.Fatalf("want version 42, got %d", resp.Version)
	}
}

func TestHandleGet_MissingKey(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/get", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleGet_BadMethod(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/get?key=k", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHandleSet_Success(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body, _ := json.Marshal(SetRequest{Key: "k", Value: []byte("v"), TTL: time.Minute, Version: 1})
	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	e, ok := srv.store.Get("k")
	if !ok || string(e.Value) != "v" {
		t.Fatal("expected value to be stored in the underlying store")
	}
}

func TestHandleSet_MissingKey(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body, _ := json.Marshal(SetRequest{Key: "", Value: []byte("v")})
	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleSet_BadBody(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader([]byte("not-json")))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleSet_BadMethod(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/set", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHandleDelete_Success(t *testing.T) {
	srv := newTestServer()
	srv.store.Set("k", []byte("v"), time.Minute, 1)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/delete?key=k", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp DeleteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Deleted {
		t.Fatal("expected deleted=true")
	}
}

func TestHandleDelete_NotFound(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/delete?key=ghost", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp DeleteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Deleted {
		t.Fatal("expected deleted=false for non-existent key")
	}
}

func TestHandleDelete_MissingKey(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/delete", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleDelete_BadMethod(t *testing.T) {
	srv := newTestServer()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/delete?key=k", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

// TestFlightGroup_StampedeProtection fires 50 concurrent calls for the same key
// and asserts the underlying fn is invoked exactly once (singleflight behaviour).
func TestFlightGroup_StampedeProtection(t *testing.T) {
	const goroutines = 50
	var dbCalls atomic.Int64

	var fg flightGroup
	var wg sync.WaitGroup
	start := make(chan struct{}) // all goroutines start at the same time

	results := make([][]byte, goroutines)
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			val, err := fg.do("user:1", func() ([]byte, error) {
				dbCalls.Add(1)
				time.Sleep(10 * time.Millisecond) // simulate slow DB query
				return []byte(`{"id":1}`), nil
			})
			if err != nil {
				t.Errorf("goroutine %d: unexpected error: %v", idx, err)
			}
			results[idx] = val
		}(i)
	}

	close(start) // release all goroutines simultaneously
	wg.Wait()

	if n := dbCalls.Load(); n != 1 {
		t.Errorf("expected DB called exactly once, got %d", n)
	}
	for i, val := range results {
		if string(val) != `{"id":1}` {
			t.Errorf("goroutine %d: got unexpected value %q", i, val)
		}
	}
}
