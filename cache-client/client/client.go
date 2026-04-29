// Package client provides a high-level cache client for services.
// It handles: cache-aside reads, stampede protection, local hot-key cache,
// TTL jitter, and circuit-breaker-style fallback to source.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/distributed-cache/cache-client/localcache"
	"github.com/distributed-cache/cache-client/stampede"
)

// Loader is a function that loads a value from the source of truth (e.g. DB).
type Loader func(ctx context.Context, key string) ([]byte, error)

// Options configures the cache client.
type Options struct {
	// RouterAddr is the router service address (e.g. "http://router:9000").
	RouterAddr string

	// DefaultTTL is the TTL applied when none is given. Default 5 minutes.
	DefaultTTL time.Duration

	// TTLJitter adds random jitter up to this value to avoid stampede expiry.
	// Default 30 seconds.
	TTLJitter time.Duration

	// LocalCacheSize is the number of entries in the in-process hot-key cache.
	// Set 0 to disable. Default 512.
	LocalCacheSize int

	// LocalCacheTTL is how long a hot key lives in the local cache. Default 5s.
	LocalCacheTTL time.Duration

	// HTTPTimeout for requests to the router. Default 500ms.
	HTTPTimeout time.Duration
}

func (o *Options) applyDefaults() {
	if o.DefaultTTL == 0 {
		o.DefaultTTL = 5 * time.Minute
	}
	if o.TTLJitter == 0 {
		o.TTLJitter = 30 * time.Second
	}
	if o.LocalCacheSize == 0 {
		o.LocalCacheSize = 512
	}
	if o.LocalCacheTTL == 0 {
		o.LocalCacheTTL = 5 * time.Second
	}
	if o.HTTPTimeout == 0 {
		o.HTTPTimeout = 500 * time.Millisecond
	}
}

// Client is the main cache client used by application services.
type Client struct {
	opts    Options
	http    *http.Client
	local   *localcache.Cache
	sflight stampede.Group
}

// New creates a Client with the given options.
func New(opts Options) *Client {
	opts.applyDefaults()
	return &Client{
		opts:  opts,
		http:  &http.Client{Timeout: opts.HTTPTimeout},
		local: localcache.New(opts.LocalCacheSize),
	}
}

// Get retrieves a key from the cache.
// On a miss it calls loader (if provided) and populates the cache (cache-aside).
// loader may be nil, in which case a miss returns (nil, false, nil).
func (c *Client) Get(ctx context.Context, key string, loader Loader) ([]byte, bool, error) {
	// 1. Check local hot-key cache.
	if v, ok := c.local.Get(key); ok {
		return v, true, nil
	}

	// 2. Stampede protection: deduplicate concurrent fetches for the same key.
	val, isLeader, err := c.sflight.Do(ctx, key, func() ([]byte, error) {
		return c.fetchOrLoad(ctx, key, loader)
	})
	if err != nil {
		return nil, false, err
	}
	_ = isLeader

	if val == nil {
		return nil, false, nil
	}

	// Populate local cache for hot keys.
	c.local.Set(key, val, c.opts.LocalCacheTTL)
	return val, true, nil
}

// fetchOrLoad tries the distributed cache first; on miss, calls loader and sets cache.
func (c *Client) fetchOrLoad(ctx context.Context, key string, loader Loader) ([]byte, error) {
	resp, err := c.remoteGet(ctx, key)
	if err == nil && resp.Hit {
		return resp.Value, nil
	}
	if err != nil {
		// Cache unavailable: fail open, go straight to source.
		fmt.Printf("cache-client: GET %s remote error (failing open): %v\n", key, err)
	}

	if loader == nil {
		return nil, nil
	}

	// Cache miss: load from source.
	value, err := loader(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("cache-client: loader error for key %s: %w", key, err)
	}
	if value == nil {
		return nil, nil
	}

	// Populate cache with jittered TTL.
	ttl := c.jitteredTTL(c.opts.DefaultTTL)
	if setErr := c.Set(ctx, key, value, ttl); setErr != nil {
		fmt.Printf("cache-client: SET %s after load error: %v\n", key, setErr)
	}
	return value, nil
}

// Set stores a key/value in the distributed cache.
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.remoteSet(ctx, key, value, ttl, time.Now().UnixNano())
}

// Delete removes a key from the distributed cache and local cache.
func (c *Client) Delete(ctx context.Context, key string) error {
	c.local.Delete(key)
	return c.remoteDelete(ctx, key)
}

// InvalidateLocal removes a key only from the local hot-key cache.
func (c *Client) InvalidateLocal(key string) {
	c.local.Delete(key)
}

// --- HTTP helpers ---

type getResponse struct {
	Key     string `json:"key"`
	Value   []byte `json:"value,omitempty"`
	Version int64  `json:"version,omitempty"`
	Hit     bool   `json:"hit"`
}

type setRequest struct {
	Key     string        `json:"key"`
	Value   []byte        `json:"value"`
	TTL     time.Duration `json:"ttl"`
	Version int64         `json:"version"`
}

func (c *Client) remoteGet(ctx context.Context, key string) (*getResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/get?key=%s", c.opts.RouterAddr, key), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("router GET status %d", resp.StatusCode)
	}
	var out getResponse
	return &out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *Client) remoteSet(ctx context.Context, key string, value []byte, ttl time.Duration, version int64) error {
	body, _ := json.Marshal(setRequest{Key: key, Value: value, TTL: ttl, Version: version})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/set", c.opts.RouterAddr), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("router SET status %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (c *Client) remoteDelete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("%s/delete?key=%s", c.opts.RouterAddr, key), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("router DELETE status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) jitteredTTL(base time.Duration) time.Duration {
	if c.opts.TTLJitter <= 0 {
		return base
	}
	//nolint:gosec // jitter does not require cryptographic randomness
	jitter := time.Duration(rand.Int63n(int64(c.opts.TTLJitter)))
	return base + jitter
}
