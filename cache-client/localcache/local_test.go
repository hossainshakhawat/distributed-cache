package localcache

import (
	"testing"
	"time"
)

func TestNew_DefaultMaxKeys(t *testing.T) {
	c := New(0)
	if c.maxKeys != 512 {
		t.Fatalf("expected maxKeys=512, got %d", c.maxKeys)
	}
}

func TestGet_Miss(t *testing.T) {
	c := New(10)
	_, ok := c.Get("missing")
	if ok {
		t.Fatal("expected miss for unknown key")
	}
}

func TestSet_And_Get(t *testing.T) {
	c := New(10)
	c.Set("key", []byte("value"), time.Minute)
	v, ok := c.Get("key")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(v) != "value" {
		t.Fatalf("want 'value', got %q", v)
	}
}

func TestGet_TTLExpiry(t *testing.T) {
	c := New(10)
	c.Set("k", []byte("v"), 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	_, ok := c.Get("k")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestSet_ZeroTTL_NeverExpires(t *testing.T) {
	c := New(10)
	c.Set("k", []byte("persistent"), 0)
	v, ok := c.Get("k")
	if !ok || string(v) != "persistent" {
		t.Fatal("expected persistent entry with zero TTL")
	}
}

func TestDelete_ExistingKey(t *testing.T) {
	c := New(10)
	c.Set("k", []byte("v"), time.Minute)
	c.Delete("k")
	_, ok := c.Get("k")
	if ok {
		t.Fatal("expected miss after delete")
	}
}

func TestDelete_NonExistentKey(t *testing.T) {
	c := New(10)
	c.Delete("does-not-exist") // must not panic
}

func TestEviction_AtCapacity(t *testing.T) {
	c := New(2)
	c.Set("a", []byte("1"), time.Minute)
	c.Set("b", []byte("2"), time.Minute)
	c.Set("c", []byte("3"), time.Minute) // triggers eviction of one existing key

	c.mu.Lock()
	n := len(c.items)
	c.mu.Unlock()

	if n != 2 {
		t.Fatalf("expected 2 items after eviction, got %d", n)
	}
	// Newly inserted key must survive eviction.
	v, ok := c.Get("c")
	if !ok || string(v) != "3" {
		t.Fatal("expected 'c' to be present after eviction")
	}
}

func TestSet_Overwrite(t *testing.T) {
	c := New(10)
	c.Set("k", []byte("v1"), time.Minute)
	c.Set("k", []byte("v2"), time.Minute)
	v, ok := c.Get("k")
	if !ok || string(v) != "v2" {
		t.Fatalf("expected overwritten value 'v2', got %q", v)
	}
}
