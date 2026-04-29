package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/hossainshakhawat/distributed-cache/cache-node/store"
)

// Loader is called by the server on a cache miss to fetch the value directly
// from the source of truth (PostgreSQL). Returning (nil, nil) means "not found".
type Loader func(key string) ([]byte, error)

// Server exposes the Store over HTTP.
type Server struct {
	store  *store.Store
	loader Loader // optional; called on miss to populate the store
}

// New creates a new HTTP server wrapping the given store.
// loader may be nil, in which case misses are returned as-is.
func New(s *store.Store, loader Loader) *Server {
	return &Server{store: s, loader: loader}
}

// RegisterRoutes wires HTTP handlers.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/get", s.handleGet)
	mux.HandleFunc("/set", s.handleSet)
	mux.HandleFunc("/delete", s.handleDelete)
	mux.HandleFunc("/health", s.handleHealth)
}

// GetRequest is sent by the router for a GET.
type GetRequest struct {
	Key string `json:"key"`
}

// GetResponse is returned for a GET.
type GetResponse struct {
	Key     string `json:"key"`
	Value   []byte `json:"value,omitempty"`
	Version int64  `json:"version,omitempty"`
	Hit     bool   `json:"hit"`
}

// SetRequest is sent by the router for a SET.
type SetRequest struct {
	Key     string        `json:"key"`
	Value   []byte        `json:"value"`
	TTL     time.Duration `json:"ttl"` // nanoseconds
	Version int64         `json:"version"`
}

// DeleteRequest is sent for a DELETE.
type DeleteRequest struct {
	Key string `json:"key"`
}

// DeleteResponse is returned for a DELETE.
type DeleteResponse struct {
	Key     string `json:"key"`
	Deleted bool   `json:"deleted"`
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	e, hit := s.store.Get(key)
	if !hit && s.loader != nil {
		// Cache miss: load from the source of truth, populate the store.
		val, err := s.loader(key)
		if err != nil {
			log.Printf("cache-node: loader %q: %v", key, err)
		} else if val != nil {
			s.store.Set(key, val, 5*time.Minute, 0)
			writeJSON(w, http.StatusOK, GetResponse{Key: key, Hit: true, Value: val})
			return
		}
	}
	resp := GetResponse{Key: key, Hit: hit}
	if hit {
		resp.Value = e.Value
		resp.Version = e.Version
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	s.store.Set(req.Key, req.Value, req.TTL, req.Version)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	deleted := s.store.Delete(key)
	writeJSON(w, http.StatusOK, DeleteResponse{Key: key, Deleted: deleted})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"keys":   strconv.Itoa(s.store.Len()),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}
