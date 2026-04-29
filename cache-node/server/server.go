package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	cachedb "github.com/hossainshakhawat/distributed-cache/cache-node/db"
	"github.com/hossainshakhawat/distributed-cache/cache-node/store"
)

// Server exposes the Store over HTTP.
type Server struct {
	store  *store.Store
	db     *cachedb.DB  // optional; nil = no DB integration
	flight flightGroup  // deduplicates concurrent DB loads for the same key
}

// New creates a new HTTP server wrapping the given store.
// db may be nil, in which case cache misses are returned as-is and
// /db/update returns 503.
func New(s *store.Store, db *cachedb.DB) *Server {
	return &Server{store: s, db: db}
}

// RegisterRoutes wires HTTP handlers.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/get", s.handleGet)
	mux.HandleFunc("/set", s.handleSet)
	mux.HandleFunc("/delete", s.handleDelete)
	mux.HandleFunc("/db/update", s.handleDbUpdate)
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
	if !hit && s.db != nil {
		// Cache miss: only one goroutine loads from DB; others wait and share
		// the result (singleflight / stampede protection).
		val, err := s.flight.do(key, func() ([]byte, error) {
			return s.db.Load(key)
		})
		if err != nil {
			log.Printf("cache-node: load %q: %v", key, err)
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

// UpdateRequest is the body for POST /db/update.
type UpdateRequest struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

func (s *Server) handleDbUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "db not configured", http.StatusServiceUnavailable)
		return
	}
	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	val, err := s.db.Update(req.Key, req.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if val == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Store the fresh value so the next GET is an immediate hit.
	s.store.Set(req.Key, val, 5*time.Minute, 0)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(val)
}

// flightGroup deduplicates concurrent DB loads for the same key (singleflight).
// When multiple goroutines miss the cache for the same key simultaneously, only
// the first one calls the DB; the rest block and receive the same result.
type flightGroup struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

type flightCall struct {
	wg  sync.WaitGroup
	val []byte
	err error
}

func (fg *flightGroup) do(key string, fn func() ([]byte, error)) ([]byte, error) {
	fg.mu.Lock()
	if fg.calls == nil {
		fg.calls = make(map[string]*flightCall)
	}
	if c, ok := fg.calls[key]; ok {
		// In-flight load for this key already running — wait for it.
		fg.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &flightCall{}
	c.wg.Add(1)
	fg.calls[key] = c
	fg.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	fg.mu.Lock()
	delete(fg.calls, key)
	fg.mu.Unlock()

	return c.val, c.err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}
