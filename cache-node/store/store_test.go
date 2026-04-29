package store

import (
	"testing"
	"time"
)

func TestGet_Miss(t *testing.T) {
	s := New(10, PolicyLRU)
	_, ok := s.Get("missing")
	if ok {
		t.Fatal("expected miss for unknown key")
	}
}

func TestSet_And_Get(t *testing.T) {
	s := New(10, PolicyLRU)
	s.Set("k", []byte("v"), time.Minute, 7)
	e, ok := s.Get("k")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(e.Value) != "v" {
		t.Fatalf("want value 'v', got %q", e.Value)
	}
	if e.Version != 7 {
		t.Fatalf("want version 7, got %d", e.Version)
	}
}

func TestGet_TTLExpiry(t *testing.T) {
	s := New(10, PolicyLRU)
	s.Set("k", []byte("v"), 10*time.Millisecond, 1)
	time.Sleep(20 * time.Millisecond)
	_, ok := s.Get("k")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestSet_ZeroTTL_NeverExpires(t *testing.T) {
	s := New(10, PolicyLRU)
	s.Set("k", []byte("v"), 0, 1)
	_, ok := s.Get("k")
	if !ok {
		t.Fatal("expected hit for entry with no TTL")
	}
}

func TestSet_Overwrite(t *testing.T) {
	s := New(10, PolicyLRU)
	s.Set("k", []byte("v1"), time.Minute, 1)
	s.Set("k", []byte("v2"), time.Minute, 2)
	e, ok := s.Get("k")
	if !ok || string(e.Value) != "v2" || e.Version != 2 {
		t.Fatalf("expected overwritten value 'v2' v2, got %q v%d ok=%v", e.Value, e.Version, ok)
	}
}

func TestDelete_ExistingKey(t *testing.T) {
	s := New(10, PolicyLRU)
	s.Set("k", []byte("v"), time.Minute, 1)
	deleted := s.Delete("k")
	if !deleted {
		t.Fatal("expected Delete to return true for existing key")
	}
	_, ok := s.Get("k")
	if ok {
		t.Fatal("expected miss after delete")
	}
}

func TestDelete_NonExistentKey(t *testing.T) {
	s := New(10, PolicyLRU)
	if s.Delete("ghost") {
		t.Fatal("expected Delete to return false for non-existent key")
	}
}

func TestLen(t *testing.T) {
	s := New(10, PolicyLRU)
	if s.Len() != 0 {
		t.Fatalf("expected 0 initial length, got %d", s.Len())
	}
	s.Set("a", []byte("1"), time.Minute, 1)
	s.Set("b", []byte("2"), time.Minute, 2)
	if s.Len() != 2 {
		t.Fatalf("expected 2, got %d", s.Len())
	}
}

func TestEviction_AtCapacity(t *testing.T) {
	s := New(2, PolicyLRU)
	s.Set("a", []byte("1"), time.Minute, 1)
	s.Set("b", []byte("2"), time.Minute, 2)
	s.Set("c", []byte("3"), time.Minute, 3) // triggers eviction
	if s.Len() != 2 {
		t.Fatalf("expected 2 items after eviction, got %d", s.Len())
	}
	// The newly inserted key must always be present.
	e, ok := s.Get("c")
	if !ok || string(e.Value) != "3" {
		t.Fatal("expected 'c' to remain after eviction")
	}
}

func TestIsExpired(t *testing.T) {
	e := &Entry{ExpiresAt: time.Now().Add(-time.Second)}
	if !e.IsExpired() {
		t.Fatal("expected expired entry to report as expired")
	}
	e2 := &Entry{ExpiresAt: time.Now().Add(time.Minute)}
	if e2.IsExpired() {
		t.Fatal("expected future entry to report as not expired")
	}
	e3 := &Entry{} // zero ExpiresAt — no expiry
	if e3.IsExpired() {
		t.Fatal("expected zero-expiry entry to never expire")
	}
}
