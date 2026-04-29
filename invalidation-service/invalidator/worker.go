// Package invalidator consumes invalidation events and deletes keys from the cache.
package invalidator

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/distributed-cache/invalidation-service/queue"
)

// Worker reads events from the bus and issues DELETE calls to the router.
type Worker struct {
	routerAddr string
	bus        *queue.Bus
	http       *http.Client
}

// New creates an invalidation Worker.
func New(routerAddr string, bus *queue.Bus) *Worker {
	return &Worker{
		routerAddr: routerAddr,
		bus:        bus,
		http:       &http.Client{Timeout: 500 * time.Millisecond},
	}
}

// Run starts consuming events until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	events := w.bus.Subscribe(256)
	log.Println("invalidation-worker: started")
	for {
		select {
		case <-ctx.Done():
			log.Println("invalidation-worker: stopping")
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			w.processEvent(ctx, ev)
		}
	}
}

func (w *Worker) processEvent(ctx context.Context, ev queue.InvalidationEvent) {
	for _, key := range ev.Keys {
		if err := w.deleteKey(ctx, key); err != nil {
			log.Printf("invalidation-worker: DELETE %s from %s: %v", key, ev.Source, err)
		} else {
			log.Printf("invalidation-worker: invalidated %s (source=%s)", key, ev.Source)
		}
	}
}

func (w *Worker) deleteKey(ctx context.Context, key string) error {
	url := fmt.Sprintf("%s/delete?key=%s", w.routerAddr, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	resp, err := w.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("router DELETE returned %d", resp.StatusCode)
	}
	return nil
}
