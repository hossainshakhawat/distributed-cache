package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newFakeCacheNode starts an httptest server that simulates a cache node.
// When hit is true, GET returns a hit with the given value; DELETE reports deleted=true.
func newFakeCacheNode(hit bool, value []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/get":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"key":   r.URL.Query().Get("key"),
				"value": value,
				"hit":   hit,
			})
		case "/set":
			w.WriteHeader(http.StatusNoContent)
		case "/delete":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"key":     r.URL.Query().Get("key"),
				"deleted": hit,
			})
		case "/health":
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
		}
	}))
}

func TestRouterHandleHealth(t *testing.T) {
	node := newFakeCacheNode(false, nil)
	defer node.Close()

	router := New([]string{node.URL})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestRouterHandleGet_Hit(t *testing.T) {
	node := newFakeCacheNode(true, []byte("cached-value"))
	defer node.Close()

	router := New([]string{node.URL})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/get?key=mykey", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp getResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Hit {
		t.Fatal("expected hit=true")
	}
}

func TestRouterHandleGet_Miss(t *testing.T) {
	node := newFakeCacheNode(false, nil)
	defer node.Close()

	router := New([]string{node.URL})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/get?key=absent", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp getResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Hit {
		t.Fatal("expected hit=false")
	}
}

func TestRouterHandleGet_MissingKey(t *testing.T) {
	router := New([]string{})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/get", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRouterHandleGet_BadMethod(t *testing.T) {
	router := New([]string{})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/get?key=k", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestRouterHandleSet_Success(t *testing.T) {
	node := newFakeCacheNode(false, nil)
	defer node.Close()

	router := New([]string{node.URL})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	body, _ := json.Marshal(setRequest{Key: "k", Value: []byte("v"), TTL: time.Minute, Version: 1})
	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRouterHandleSet_NoNodes(t *testing.T) {
	router := New([]string{})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	body, _ := json.Marshal(setRequest{Key: "k", Value: []byte("v")})
	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no nodes, got %d", rr.Code)
	}
}

func TestRouterHandleSet_MissingKey(t *testing.T) {
	node := newFakeCacheNode(false, nil)
	defer node.Close()

	router := New([]string{node.URL})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	body, _ := json.Marshal(setRequest{Key: "", Value: []byte("v")})
	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRouterHandleSet_BadMethod(t *testing.T) {
	router := New([]string{})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/set", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestRouterHandleDelete_Success(t *testing.T) {
	node := newFakeCacheNode(true, nil) // hit=true → deleted=true
	defer node.Close()

	router := New([]string{node.URL})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/delete?key=k", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp deleteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Deleted {
		t.Fatal("expected deleted=true")
	}
}

func TestRouterHandleDelete_MissingKey(t *testing.T) {
	router := New([]string{})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/delete", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRouterHandleDelete_BadMethod(t *testing.T) {
	router := New([]string{})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/delete?key=k", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestRouterHandleNodes(t *testing.T) {
	node := newFakeCacheNode(false, nil)
	defer node.Close()

	router := New([]string{node.URL})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/nodes", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["nodes"]; !ok {
		t.Fatal("expected 'nodes' field in response")
	}
}

func TestRouterAddRemoveNode(t *testing.T) {
	node := newFakeCacheNode(true, []byte("v"))
	defer node.Close()

	router := New([]string{node.URL})

	// Remove the node — ring becomes empty.
	router.RemoveNode(node.URL)
	if nodes := router.ring.Nodes(); len(nodes) != 0 {
		t.Fatalf("expected empty ring after RemoveNode, got %v", nodes)
	}

	// Add it back.
	router.AddNode(node.URL)
	if nodes := router.ring.Nodes(); len(nodes) != 1 {
		t.Fatalf("expected 1 node after AddNode, got %v", nodes)
	}
}
