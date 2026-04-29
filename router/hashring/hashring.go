// Package hashring implements consistent hashing with virtual nodes.
package hashring

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

const defaultVirtualNodes = 150

// Ring is a thread-safe consistent hash ring.
type Ring struct {
	mu           sync.RWMutex
	virtualNodes int
	ring         map[uint32]string // hash → node address
	sorted       []uint32
}

// New creates a Ring with the given number of virtual nodes per physical node.
func New(virtualNodes int) *Ring {
	if virtualNodes <= 0 {
		virtualNodes = defaultVirtualNodes
	}
	return &Ring{
		virtualNodes: virtualNodes,
		ring:         make(map[uint32]string),
	}
}

// AddNode registers a node and its virtual replicas.
func (r *Ring) AddNode(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := 0; i < r.virtualNodes; i++ {
		h := hashKey(fmt.Sprintf("%s#vn%d", addr, i))
		r.ring[h] = addr
		r.sorted = append(r.sorted, h)
	}
	sort.Slice(r.sorted, func(a, b int) bool { return r.sorted[a] < r.sorted[b] })
}

// RemoveNode deregisters a node and all its virtual replicas.
func (r *Ring) RemoveNode(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := 0; i < r.virtualNodes; i++ {
		h := hashKey(fmt.Sprintf("%s#vn%d", addr, i))
		delete(r.ring, h)
	}
	// Rebuild sorted slice.
	r.sorted = r.sorted[:0]
	for h := range r.ring {
		r.sorted = append(r.sorted, h)
	}
	sort.Slice(r.sorted, func(a, b int) bool { return r.sorted[a] < r.sorted[b] })
}

// GetNode returns the node responsible for the given key.
// Returns "" if the ring is empty.
func (r *Ring) GetNode(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.sorted) == 0 {
		return ""
	}
	h := hashKey(key)
	idx := sort.Search(len(r.sorted), func(i int) bool {
		return r.sorted[i] >= h
	})
	if idx == len(r.sorted) {
		idx = 0
	}
	return r.ring[r.sorted[idx]]
}

// GetNodes returns up to n distinct nodes starting from the key's position.
// Used for replication: primary is index 0, replicas follow.
func (r *Ring) GetNodes(key string, n int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.sorted) == 0 || n <= 0 {
		return nil
	}
	h := hashKey(key)
	idx := sort.Search(len(r.sorted), func(i int) bool {
		return r.sorted[i] >= h
	})

	seen := make(map[string]bool)
	var result []string
	for i := 0; i < len(r.sorted) && len(result) < n; i++ {
		pos := (idx + i) % len(r.sorted)
		addr := r.ring[r.sorted[pos]]
		if !seen[addr] {
			seen[addr] = true
			result = append(result, addr)
		}
	}
	return result
}

// Nodes returns all distinct physical nodes currently registered.
func (r *Ring) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]bool)
	var out []string
	for _, addr := range r.ring {
		if !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	return out
}

func hashKey(key string) uint32 {
	h := sha256.Sum256([]byte(key))
	return binary.BigEndian.Uint32(h[:4])
}
