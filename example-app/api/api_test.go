package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hossainshakhawat/distributed-cache/cache-client/client"
)

// newFakeRouter starts an httptest server simulating the distributed-cache router.
//
//   - GET  /get         — returns a cache hit for user:1-10 (read-through simulation)
//   - POST /db/update   — updates a user; returns 200+JSON for user:1-10, 404 for others
//   - POST /set         — no-op, 204
//   - DELETE /delete    — no-op, returns deleted:true
func newFakeRouter() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/get":
			key := r.URL.Query().Get("key")
			var hit bool
			var val []byte
			if strings.HasPrefix(key, "user:") {
				id, err := strconv.Atoi(strings.TrimPrefix(key, "user:"))
				if err == nil && id >= 1 && id <= 10 {
					hit = true
					val, _ = json.Marshal(map[string]any{"id": id, "name": "User " + strconv.Itoa(id)})
				}
			}
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"key": key, "value": val, "hit": hit,
			})
		case "/db/update":
			var req struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			id, err := strconv.Atoi(strings.TrimPrefix(req.Key, "user:"))
			if err != nil || id < 1 || id > 10 {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"id": id, "name": req.Name,
			})
		case "/set":
			w.WriteHeader(http.StatusNoContent)
		case "/delete":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"key": r.URL.Query().Get("key"), "deleted": true,
			})
		}
	}))
}

// newTestHandler builds a Handler wired to the given fake router and invalidation server.
func newTestHandler(routerURL, invalidationURL string) *Handler {
	cacheClient := client.New(client.Options{
		RouterAddr:     routerURL,
		LocalCacheSize: 10,
		LocalCacheTTL:  time.Millisecond,
		HTTPTimeout:    time.Second,
	})
	return New(cacheClient, routerURL, invalidationURL)
}

func TestGetUser_CacheMiss_DBHit(t *testing.T) {
	router := newFakeRouter()
	defer router.Close()

	h := newTestHandler(router.URL, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/users/1", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var u struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 {
		t.Fatalf("expected user ID 1, got %d", u.ID)
	}
}

func TestGetUser_CacheHit(t *testing.T) {
	router := newFakeRouter()
	defer router.Close()

	h := newTestHandler(router.URL, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/users/5", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if rr.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("expected X-Cache: HIT, got %q", rr.Header().Get("X-Cache"))
	}
}

func TestGetUser_InvalidID(t *testing.T) {
	router := newFakeRouter()
	defer router.Close()

	h := newTestHandler(router.URL, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, path := range []string{"/users/abc", "/users/0", "/users/-1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("path %q: expected 400, got %d", path, rr.Code)
		}
	}
}

func TestGetUser_NotFound(t *testing.T) {
	router := newFakeRouter()
	defer router.Close()

	h := newTestHandler(router.URL, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// user:9999 is outside range 1-10 — cache-node returns miss, no value.
	req := httptest.NewRequest(http.MethodGet, "/users/9999", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestUpdateUser_Success(t *testing.T) {
	router := newFakeRouter()
	defer router.Close()

	var invalidationReceived bool
	invalidSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/invalidate" {
			invalidationReceived = true
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer invalidSrv.Close()

	h := newTestHandler(router.URL, invalidSrv.URL)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body, _ := json.Marshal(map[string]string{"name": "Updated"})
	req := httptest.NewRequest(http.MethodPut, "/users/1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// Verify the response body contains the updated name.
	var u struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.Name != "Updated" {
		t.Fatalf("expected name 'Updated', got %q", u.Name)
	}
	if !invalidationReceived {
		t.Fatal("expected invalidation event to be forwarded to invalidation service")
	}
}

func TestUpdateUser_BadBody(t *testing.T) {
	router := newFakeRouter()
	defer router.Close()

	h := newTestHandler(router.URL, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPut, "/users/1", bytes.NewReader([]byte("not-json")))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUpdateUser_NotFound(t *testing.T) {
	router := newFakeRouter()
	defer router.Close()

	h := newTestHandler(router.URL, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body, _ := json.Marshal(map[string]string{"name": "X"})
	req := httptest.NewRequest(http.MethodPut, "/users/9999", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestHandleUser_MethodNotAllowed(t *testing.T) {
	router := newFakeRouter()
	defer router.Close()

	h := newTestHandler(router.URL, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/users/1", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	router := newFakeRouter()
	defer router.Close()

	h := newTestHandler(router.URL, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
