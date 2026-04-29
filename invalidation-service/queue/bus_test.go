package queue

import (
	"context"
	"testing"
	"time"
)

func TestSubscribe_And_Publish(t *testing.T) {
	b := NewBus()
	ch := b.Subscribe(10)

	ev := InvalidationEvent{Keys: []string{"k1", "k2"}, Source: "test"}
	b.Publish(ev)

	select {
	case got := <-ch:
		if len(got.Keys) != 2 || got.Keys[0] != "k1" || got.Keys[1] != "k2" {
			t.Fatalf("unexpected keys: %v", got.Keys)
		}
		if got.Source != "test" {
			t.Fatalf("unexpected source: %q", got.Source)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for event")
	}
}

func TestPublish_MultipleSubscribers(t *testing.T) {
	b := NewBus()
	ch1 := b.Subscribe(10)
	ch2 := b.Subscribe(10)

	b.Publish(InvalidationEvent{Keys: []string{"x"}, Source: "multi"})

	for i, ch := range []<-chan InvalidationEvent{ch1, ch2} {
		select {
		case got := <-ch:
			if got.Keys[0] != "x" {
				t.Fatalf("subscriber %d: unexpected key %q", i, got.Keys[0])
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("subscriber %d: timeout waiting for event", i)
		}
	}
}

func TestPublish_DropsOnFullSubscriber(t *testing.T) {
	b := NewBus()
	ch := b.Subscribe(1) // buffer depth of 1

	// First event fills the buffer; second is dropped.
	b.Publish(InvalidationEvent{Keys: []string{"a"}, Source: "s"})
	b.Publish(InvalidationEvent{Keys: []string{"b"}, Source: "s"})

	if len(ch) != 1 {
		t.Fatalf("expected exactly 1 buffered event, got %d", len(ch))
	}
}

func TestDrainAndClose(t *testing.T) {
	b := NewBus()
	ch := b.Subscribe(5)

	b.DrainAndClose(context.Background())

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after DrainAndClose")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestPublish_NoSubscribers(t *testing.T) {
	b := NewBus()
	// Must not panic or block.
	b.Publish(InvalidationEvent{Keys: []string{"k"}, Source: "s"})
}
