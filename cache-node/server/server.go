package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/hossainshakhawat/distributed-cache/cache-node/store"
)

// Server exposes the Store over HTTP.
type Server struct {
	store      *store.Store
	loaderURL  string        // optional base URL of the source-of-truth loader
	defaultTTL time.Duration // TTL applied to read-through entries
	httpClient *http.Client
}

// New creates a new HTTP server wrapping the given store.
// loaderURL is the optional base URL of a service that implements
// GET /db/load?key=<key> (e.g. "http://example-app:8888").
// When set, cache misses trigger a read-through call to that service.
// defaultTTL controls how long read-through values are cached; 0 uses 5 minutes.
func New(s *store.Store, loaderURL string, defaultTTL time.Duration) *Server {
	if defaultTTL == 0 {
		defaultTTL = 5 * time.Minute
	}
	return &Server{
		store:      s,
		loaderURL:  loaderURL,
		defaultTTL: defaultTTL,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
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
	if !hit && s.loaderURL != "" {
		// Read-through: fetch from the source of truth and populate the store.
		if val, ok := s.loadFromSource(key); ok {
			s.store.Set(key, val, s.defaultTTL, 0)
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

// loadFromSource fetches the value for key from the configured loader service.
func (s *Server) loadFromSource(key string) ([]byte, bool) {
	u, err := url.Parse(s.loaderURL + "/db/load")
	if err != nil {
		log.Printf("cache-node: loader: invalid URL: %v", err)
		return nil, false
	}
	u.RawQuery = url.Values{"key": {key}}.Encode()
	resp, err := s.httpClient.Get(u.String())
	if err != nil {
		log.Printf("cache-node: loader GET %q: %v", key, err)
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	val, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("cache-node: loader read %q: %v", key, err)
		return nil, false
	}
	return val, true
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
