// Package server exposes the invalidation service's HTTP API.
// Services POST events here; the worker deletes the keys from the cache.
package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/hossainshakhawat/distributed-cache/invalidation-service/queue"
)

// Server accepts invalidation events over HTTP and forwards them to the Bus.
type Server struct {
	bus *queue.Bus
}

// New creates an invalidation HTTP server.
func New(bus *queue.Bus) *Server { return &Server{bus: bus} }

// RegisterRoutes wires HTTP handlers.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/invalidate", s.handleInvalidate)
	mux.HandleFunc("/health", s.handleHealth)
}

type invalidateRequest struct {
	Keys   []string `json:"keys"`
	Source string   `json:"source"`
}

func (s *Server) handleInvalidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req invalidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Keys) == 0 {
		http.Error(w, "keys required", http.StatusBadRequest)
		return
	}
	s.bus.Publish(queue.InvalidationEvent{Keys: req.Keys, Source: req.Source})
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		log.Printf("handleHealth encode: %v", err)
	}
}
