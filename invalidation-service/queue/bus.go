// Package queue provides an in-process channel-based event bus.
// In production this would be replaced by Kafka/NATS/Redis Pub-Sub.
package queue

import (
	"context"
	"sync"
)

// InvalidationEvent represents a request to delete one or more cache keys.
type InvalidationEvent struct {
	Keys   []string `json:"keys"`
	Source string   `json:"source"` // which service emitted the event
}

// Bus is a simple fan-out publish/subscribe bus.
type Bus struct {
	mu   sync.RWMutex
	subs []chan InvalidationEvent
}

// NewBus creates a Bus.
func NewBus() *Bus { return &Bus{} }

// Subscribe returns a channel that receives events. bufferSize controls channel depth.
func (b *Bus) Subscribe(bufferSize int) <-chan InvalidationEvent {
	ch := make(chan InvalidationEvent, bufferSize)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

// Publish sends an event to all subscribers, non-blocking (drops if subscriber is full).
func (b *Bus) Publish(e InvalidationEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			// subscriber slow; drop to avoid blocking publisher
		}
	}
}

// DrainAndClose closes all subscriber channels (graceful shutdown).
func (b *Bus) DrainAndClose(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		close(ch)
	}
	b.subs = nil
}
