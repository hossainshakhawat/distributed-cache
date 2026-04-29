package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hossainshakhawat/distributed-cache/cache-client/client"
	"github.com/hossainshakhawat/distributed-cache/example-app/db"
)

// newFakeRouter starts an httptest server that simulates the distributed-cache router.
// On GET /get the router simulates cache-node read-through: if the key is
// "user:{1-10}" it returns the user JSON as a hit (mirroring what a real
// cache-node with a PostgreSQL loader would do).
func newFakeRouter() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/get":
			key := r.URL.Query().Get("key")
			var idStr string
			var hit bool
			var val []byte
			if strings.HasPrefix(key, "user:") {
				id, err := strconv.Atoi(strings.TrimPrefix(key, "user:"))
				if err == nil && id >= 1 && id <= 10 {
					hit = true
					idStr = strconv.Itoa(id)
					val, _ = json.Marshal(map[string]any{"id": id, "name": "User " + idStr})
				}
			}
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"key": key, "value": val, "hit": hit,
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

// newTestHandler builds a Handler with an in-memory DB and a cache client pointed at routerURL.
func newTestHandler(routerURL, invalidationURL string) (*Handler, db.Store) {
	database := db.NewInMemory()
	cacheClient := client.New(client.Options{
		RouterAddr:     routerURL,
		LocalCacheSize: 10,
		LocalCacheTTL:  time.Millisecond,
		HTTPTimeout:    time.Second,
	})
	return New(cacheClient, database, invalidationURL), database
}

func TestGetUser_CacheMiss_DBHit(t *testing.T) {
	router := newFakeRouter()
	defer router.Close()

	h, _ := newTestHandler(router.URL, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/users/1", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var u db.User
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

	h, _ := newTestHandler(router.URL, "")
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

	h, _ := newTestHandler(router.URL, "")
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

	h, _ := newTestHandler(router.URL, "")
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

	h, database := newTestHandler(router.URL, invalidSrv.URL)
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
	// Verify DB was actually updated.
	u, err := database.GetUser(context.Background(), 1)
	if err != nil {
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

	h, _ := newTestHandler(router.URL, "")
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

	h, _ := newTestHandler(router.URL, "")
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

	h, _ := newTestHandler(router.URL, "")
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

	h, _ := newTestHandler(router.URL, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
