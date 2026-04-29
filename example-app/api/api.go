// Package api provides an HTTP API that demonstrates cache-aside pattern.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/hossainshakhawat/distributed-cache/cache-client/client"
)

// Handler serves user endpoints backed by the distributed cache cluster.
type Handler struct {
	cache            *client.Client
	routerAddr       string
	invalidationAddr string
	httpClient       *http.Client
}

// New creates an API Handler.
// routerAddr is used for write-through updates (POST /db/update).
func New(cache *client.Client, routerAddr, invalidationAddr string) *Handler {
	return &Handler{
		cache:            cache,
		routerAddr:       routerAddr,
		invalidationAddr: invalidationAddr,
		httpClient:       &http.Client{Timeout: time.Second},
	}
}

// RegisterRoutes wires HTTP handlers.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/users/", h.handleUser)
	mux.HandleFunc("/health", h.handleHealth)
}

func (h *Handler) handleUser(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/users/"):]
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getUser(w, r, id)
	case http.MethodPut:
		h.updateUser(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request, id int) {
	cacheKey := fmt.Sprintf("user:%d", id)

	// Cache-aside: the cache-node loads from PostgreSQL on a miss.
	// No loader closure needed here.
	val, hit, err := h.cache.Get(r.Context(), cacheKey, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if val == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", cacheHitHeader(hit))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(val)
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request, id int) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("user:%d", id)

	// Write to PostgreSQL and refresh the cache via router → cache-node.
	payload, _ := json.Marshal(map[string]string{"key": cacheKey, "name": body.Name})
	updReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		h.routerAddr+"/db/update", bytes.NewReader(payload))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updReq.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(updReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	updated, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}

	// Publish invalidation so other cache-nodes evict the stale entry.
	h.publishInvalidation(r.Context(), cacheKey)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(updated)
}

// publishInvalidation sends an invalidation event to the invalidation service.
func (h *Handler) publishInvalidation(ctx context.Context, keys ...string) {
	if h.invalidationAddr == "" {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"keys":   keys,
		"source": "example-app",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.invalidationAddr+"/invalidate", bytes.NewReader(payload))
	if err != nil {
		log.Printf("api: invalidation request build: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		log.Printf("api: publish invalidation: %v", err)
		return
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func cacheHitHeader(hit bool) string {
	if hit {
		return "HIT"
	}
	return "MISS"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}
