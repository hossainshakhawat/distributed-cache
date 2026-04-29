// Package server exposes the router's HTTP API.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/hossainshakhawat/distributed-cache/router/hashring"
	"github.com/hossainshakhawat/distributed-cache/router/proxy"
)

const replicationFactor = 2

// Router forwards cache operations to the correct cache-node(s).
type Router struct {
	ring    *hashring.Ring
	clients map[string]*proxy.NodeClient // addr → client
	mu      sync.RWMutex
}

// New creates a Router with the given node addresses.
func New(nodeAddrs []string) *Router {
	ring := hashring.New(150)
	clients := make(map[string]*proxy.NodeClient, len(nodeAddrs))
	for _, addr := range nodeAddrs {
		ring.AddNode(addr)
		clients[addr] = proxy.NewNodeClient(addr)
	}
	return &Router{ring: ring, clients: clients}
}

// RegisterRoutes wires HTTP handlers.
func (ro *Router) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/get", ro.handleGet)
	mux.HandleFunc("/set", ro.handleSet)
	mux.HandleFunc("/delete", ro.handleDelete)
	mux.HandleFunc("/nodes", ro.handleNodes)
	mux.HandleFunc("/health", ro.handleHealth)
}

// --- handlers ---

type setRequest struct {
	Key     string        `json:"key"`
	Value   []byte        `json:"value"`
	TTL     time.Duration `json:"ttl"`
	Version int64         `json:"version"`
}

type getResponse struct {
	Key     string `json:"key"`
	Value   []byte `json:"value,omitempty"`
	Version int64  `json:"version,omitempty"`
	Hit     bool   `json:"hit"`
}

type deleteResponse struct {
	Key     string `json:"key"`
	Deleted bool   `json:"deleted"`
}

func (ro *Router) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 400*time.Millisecond)
	defer cancel()

	// Try primary, then replicas.
	nodes := ro.ring.GetNodes(key, replicationFactor)
	for _, addr := range nodes {
		c := ro.nodeClient(addr)
		if c == nil {
			continue
		}
		resp, err := c.Get(ctx, key)
		if err != nil {
			log.Printf("router GET %s → %s: %v", key, addr, err)
			continue
		}
		writeJSON(w, http.StatusOK, getResponse{
			Key:     resp.Key,
			Value:   resp.Value,
			Version: resp.Version,
			Hit:     resp.Hit,
		})
		return
	}
	// All nodes failed or miss.
	writeJSON(w, http.StatusOK, getResponse{Key: key, Hit: false})
}

func (ro *Router) handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req setRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 400*time.Millisecond)
	defer cancel()

	// Write to primary synchronously, replicas asynchronously.
	nodes := ro.ring.GetNodes(req.Key, replicationFactor)
	if len(nodes) == 0 {
		http.Error(w, "no cache nodes available", http.StatusServiceUnavailable)
		return
	}

	// Primary write (synchronous).
	primary := nodes[0]
	if c := ro.nodeClient(primary); c != nil {
		if err := c.Set(ctx, req.Key, req.Value, req.TTL, req.Version); err != nil {
			log.Printf("router SET primary %s → %s: %v", req.Key, primary, err)
		}
	}

	// Replica writes (async).
	for _, addr := range nodes[1:] {
		addr := addr
		go func() {
			bgCtx, bgCancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer bgCancel()
			c := ro.nodeClient(addr)
			if c == nil {
				return
			}
			if err := c.Set(bgCtx, req.Key, req.Value, req.TTL, req.Version); err != nil {
				log.Printf("router SET replica %s → %s: %v", req.Key, addr, err)
			}
		}()
	}

	w.WriteHeader(http.StatusNoContent)
}

func (ro *Router) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 400*time.Millisecond)
	defer cancel()

	nodes := ro.ring.GetNodes(key, replicationFactor)
	deleted := false
	for _, addr := range nodes {
		c := ro.nodeClient(addr)
		if c == nil {
			continue
		}
		resp, err := c.Delete(ctx, key)
		if err != nil {
			log.Printf("router DELETE %s → %s: %v", key, addr, err)
			continue
		}
		if resp.Deleted {
			deleted = true
		}
	}
	writeJSON(w, http.StatusOK, deleteResponse{Key: key, Deleted: deleted})
}

func (ro *Router) handleNodes(w http.ResponseWriter, r *http.Request) {
	ro.mu.RLock()
	defer ro.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": ro.ring.Nodes(),
	})
}

func (ro *Router) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// nodeClient safely retrieves the client for an address.
func (ro *Router) nodeClient(addr string) *proxy.NodeClient {
	ro.mu.RLock()
	defer ro.mu.RUnlock()
	return ro.clients[addr]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

// AddNode dynamically adds a node to the ring at runtime.
func (ro *Router) AddNode(addr string) {
	ro.mu.Lock()
	defer ro.mu.Unlock()
	ro.ring.AddNode(addr)
	ro.clients[addr] = proxy.NewNodeClient(addr)
	fmt.Printf("router: added node %s\n", addr)
}

// RemoveNode dynamically removes a node from the ring at runtime.
func (ro *Router) RemoveNode(addr string) {
	ro.mu.Lock()
	defer ro.mu.Unlock()
	ro.ring.RemoveNode(addr)
	delete(ro.clients, addr)
	fmt.Printf("router: removed node %s\n", addr)
}
