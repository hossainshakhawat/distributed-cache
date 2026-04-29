package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient returns a Client pointing at the given test server address.
func newTestClient(addr string) *Client {
	return New(Options{
		RouterAddr:     addr,
		LocalCacheSize: 10,
		LocalCacheTTL:  time.Millisecond, // very short so local cache doesn't interfere
		HTTPTimeout:    time.Second,
	})
}

func TestGet_LocalCacheHit(t *testing.T) {
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.local.Set("hotkey", []byte("local-value"), time.Minute)

	val, hit, err := c.Get(context.Background(), "hotkey", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hit || string(val) != "local-value" {
		t.Fatalf("want hit with 'local-value', got hit=%v val=%q", hit, val)
	}
	if requestCount != 0 {
		t.Fatal("expected no HTTP requests for local cache hit")
	}
}

func TestGet_RemoteHit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/get" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(getResponse{ //nolint:errcheck
			Key:   r.URL.Query().Get("key"),
			Value: []byte("remote-value"),
			Hit:   true,
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	val, hit, err := c.Get(context.Background(), "mykey", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hit || string(val) != "remote-value" {
		t.Fatalf("want hit with 'remote-value', got hit=%v val=%q", hit, val)
	}
}

func TestGet_RemoteMiss_WithLoader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/get":
			json.NewEncoder(w).Encode(getResponse{Key: r.URL.Query().Get("key"), Hit: false}) //nolint:errcheck
		case "/set":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	var loaderCalled bool
	val, hit, err := c.Get(context.Background(), "dbkey", func(_ context.Context, _ string) ([]byte, error) {
		loaderCalled = true
		return []byte("db-value"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !loaderCalled {
		t.Fatal("expected loader to be called on cache miss")
	}
	if !hit || string(val) != "db-value" {
		t.Fatalf("want hit=true with 'db-value', got hit=%v val=%q", hit, val)
	}
}

func TestGet_RemoteMiss_NilLoader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(getResponse{Key: r.URL.Query().Get("key"), Hit: false}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	val, hit, err := c.Get(context.Background(), "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hit || val != nil {
		t.Fatalf("expected nil miss, got hit=%v val=%v", hit, val)
	}
}

func TestGet_RemoteError_FallsOpenToLoader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/get" {
			http.Error(w, "node down", http.StatusInternalServerError)
			return
		}
		// /set after loader succeeds — just accept it.
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	val, hit, err := c.Get(context.Background(), "k2", func(_ context.Context, _ string) ([]byte, error) {
		return []byte("fallback"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hit || string(val) != "fallback" {
		t.Fatalf("want hit=true val='fallback', got hit=%v val=%q", hit, val)
	}
}

func TestGet_LoaderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(getResponse{Hit: false}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, _, err := c.Get(context.Background(), "k", func(_ context.Context, _ string) ([]byte, error) {
		return nil, context.DeadlineExceeded
	})
	if err == nil {
		t.Fatal("expected error from loader")
	}
}

func TestSet_CallsRemote(t *testing.T) {
	var setCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			setCalled = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	if err := c.Set(context.Background(), "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if !setCalled {
		t.Fatal("expected /set to be called")
	}
}

func TestDelete_ClearsLocalAndCallsRemote(t *testing.T) {
	var deleteCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/delete" {
			deleteCalled = true
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.local.Set("k", []byte("v"), time.Minute)

	if err := c.Delete(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if !deleteCalled {
		t.Fatal("expected /delete to be called")
	}
	_, ok := c.local.Get("k")
	if ok {
		t.Fatal("expected local cache to be cleared after Delete")
	}
}

func TestInvalidateLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.local.Set("k", []byte("v"), time.Minute)
	c.InvalidateLocal("k")

	_, ok := c.local.Get("k")
	if ok {
		t.Fatal("expected local cache to be cleared after InvalidateLocal")
	}
}
