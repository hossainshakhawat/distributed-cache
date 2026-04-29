// Package proxy forwards cache operations to the correct cache-node.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const httpTimeout = 500 * time.Millisecond

// NodeClient talks to a single cache-node over HTTP.
type NodeClient struct {
	addr   string
	client *http.Client
}

// NewNodeClient creates a NodeClient for the given address (e.g. "http://cache-node-1:8080").
func NewNodeClient(addr string) *NodeClient {
	return &NodeClient{
		addr: addr,
		client: &http.Client{
			Timeout: httpTimeout,
		},
	}
}

// GetResponse mirrors the cache-node response.
type GetResponse struct {
	Key     string `json:"key"`
	Value   []byte `json:"value,omitempty"`
	Version int64  `json:"version,omitempty"`
	Hit     bool   `json:"hit"`
}

// SetRequest mirrors the cache-node request.
type SetRequest struct {
	Key     string        `json:"key"`
	Value   []byte        `json:"value"`
	TTL     time.Duration `json:"ttl"`
	Version int64         `json:"version"`
}

// DeleteResponse mirrors the cache-node response.
type DeleteResponse struct {
	Key     string `json:"key"`
	Deleted bool   `json:"deleted"`
}

// Get calls GET on the node.
func (n *NodeClient) Get(ctx context.Context, key string) (*GetResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/get?key=%s", n.addr, key), nil)
	if err != nil {
		return nil, err
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("node %s GET: status %d", n.addr, resp.StatusCode)
	}
	var out GetResponse
	return &out, json.NewDecoder(resp.Body).Decode(&out)
}

// Set calls SET on the node.
func (n *NodeClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration, version int64) error {
	body, _ := json.Marshal(SetRequest{Key: key, Value: value, TTL: ttl, Version: version})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/set", n.addr), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("node %s SET: status %d: %s", n.addr, resp.StatusCode, b)
	}
	return nil
}

// Delete calls DELETE on the node.
func (n *NodeClient) Delete(ctx context.Context, key string) (*DeleteResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("%s/delete?key=%s", n.addr, key), nil)
	if err != nil {
		return nil, err
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("node %s DELETE: status %d", n.addr, resp.StatusCode)
	}
	var out DeleteResponse
	return &out, json.NewDecoder(resp.Body).Decode(&out)
}

// Addr returns the node's address.
func (n *NodeClient) Addr() string { return n.addr }
