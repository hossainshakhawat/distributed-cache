package store

import (
	"sync"
	"time"
)

// Entry holds a cached value with metadata.
type Entry struct {
	Value     []byte
	CreatedAt time.Time
	ExpiresAt time.Time
	Version   int64
}

// IsExpired returns true if the entry has passed its TTL.
func (e *Entry) IsExpired() bool {
	if e.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(e.ExpiresAt)
}

// Store is a thread-safe in-memory key/value store with TTL support.
type Store struct {
	mu      sync.RWMutex
	data    map[string]*Entry
	maxKeys int
	policy  EvictionPolicy
}

// EvictionPolicy controls which key is evicted under memory pressure.
type EvictionPolicy int

const (
	PolicyLRU EvictionPolicy = iota
	PolicyLFU
)

// New creates a Store with the given capacity and eviction policy.
func New(maxKeys int, policy EvictionPolicy) *Store {
	s := &Store{
		data:    make(map[string]*Entry, maxKeys),
		maxKeys: maxKeys,
		policy:  policy,
	}
	go s.cleanupLoop()
	return s
}

// Get retrieves a value. Returns nil when missing or expired (lazy expiry).
func (s *Store) Get(key string) (*Entry, bool) {
	s.mu.RLock()
	e, ok := s.data[key]
	s.mu.RUnlock()

	if !ok {
		return nil, false
	}
	if e.IsExpired() {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
		return nil, false
	}
	return e, true
}

// Set inserts or updates a key. ttl=0 means no expiration.
func (s *Store) Set(key string, value []byte, ttl time.Duration, version int64) {
	now := time.Now()
	e := &Entry{
		Value:     value,
		CreatedAt: now,
		Version:   version,
	}
	if ttl > 0 {
		e.ExpiresAt = now.Add(ttl)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.data) >= s.maxKeys {
		s.evict()
	}
	s.data[key] = e
}

// Delete removes a key. Returns true if the key existed.
func (s *Store) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[key]
	delete(s.data, key)
	return ok
}

// Len returns the current number of entries (including expired ones not yet cleaned).
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// evict removes one entry according to the configured policy.
// Caller must hold the write lock.
func (s *Store) evict() {
	// Simple approach: evict the first expired key found, or the oldest entry.
	var (
		victimKey     string
		victimTime    = time.Now().Add(24 * time.Hour)
	)
	for k, e := range s.data {
		if e.IsExpired() {
			delete(s.data, k)
			return
		}
		if e.CreatedAt.Before(victimTime) {
			victimTime = e.CreatedAt
			victimKey = k
		}
	}
	if victimKey != "" {
		delete(s.data, victimKey)
	}
}

// cleanupLoop periodically removes expired keys.
func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		for k, e := range s.data {
			if e.IsExpired() {
				delete(s.data, k)
			}
		}
		s.mu.Unlock()
	}
}
