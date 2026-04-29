// Package stampede provides protection against cache stampede (thundering herd).
package stampede

import (
	"context"
	"sync"
)

// Group ensures only one fetch per key runs at a time.
// Other goroutines wait and share the result (singleflight pattern).
type Group struct {
	mu    sync.Mutex
	calls map[string]*call
}

type call struct {
	wg  sync.WaitGroup
	val []byte
	err error
}

// Do runs fn for key if no call is in-flight; otherwise waits and returns the shared result.
func (g *Group) Do(ctx context.Context, key string, fn func() ([]byte, error)) ([]byte, bool, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*call)
	}
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		// Wait using a channel so we respect ctx cancellation.
		done := make(chan struct{})
		go func() {
			c.wg.Wait()
			close(done)
		}()
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-done:
		}
		return c.val, false, c.err
	}
	c := &call{}
	c.wg.Add(1)
	g.calls[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()

	return c.val, true, c.err
}
