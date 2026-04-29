package stampede

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDo_SingleCall(t *testing.T) {
	var g Group
	val, leader, err := g.Do(context.Background(), "k", func() ([]byte, error) {
		return []byte("hello"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !leader {
		t.Fatal("expected to be leader on first call")
	}
	if string(val) != "hello" {
		t.Fatalf("want 'hello', got %q", val)
	}
}

func TestDo_Error(t *testing.T) {
	var g Group
	sentinel := errors.New("oops")
	_, _, err := g.Do(context.Background(), "k", func() ([]byte, error) {
		return nil, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
}

func TestDo_IndependentKeys(t *testing.T) {
	var g Group
	val1, _, _ := g.Do(context.Background(), "k1", func() ([]byte, error) { return []byte("a"), nil })
	val2, _, _ := g.Do(context.Background(), "k2", func() ([]byte, error) { return []byte("b"), nil })
	if string(val1) != "a" || string(val2) != "b" {
		t.Fatalf("independent keys should produce independent results: got %q %q", val1, val2)
	}
}

func TestDo_Deduplication(t *testing.T) {
	var g Group
	var callCount int64

	leaderStarted := make(chan struct{})
	unblock := make(chan struct{})

	const followers = 5
	results := make([]string, followers+1)
	errs := make([]error, followers+1)
	var wg sync.WaitGroup

	// Leader goroutine — holds fn in-flight.
	wg.Add(1)
	go func() {
		defer wg.Done()
		val, _, err := g.Do(context.Background(), "k", func() ([]byte, error) {
			close(leaderStarted) // signal: fn is now running
			<-unblock
			atomic.AddInt64(&callCount, 1)
			return []byte("shared"), nil
		})
		results[0] = string(val)
		errs[0] = err
	}()

	// Wait until leader's fn is in-flight.
	<-leaderStarted

	// Start followers — they should find the in-flight call and wait.
	for i := 1; i <= followers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			val, _, err := g.Do(context.Background(), "k", func() ([]byte, error) {
				// fn should NOT run for followers.
				atomic.AddInt64(&callCount, 1)
				return []byte("should-not-see"), nil
			})
			results[idx] = string(val)
			errs[idx] = err
		}(i)
	}

	// Give followers time to register as waiters before releasing leader.
	time.Sleep(10 * time.Millisecond)
	close(unblock)
	wg.Wait()

	if callCount != 1 {
		t.Fatalf("fn called %d times, expected exactly 1", callCount)
	}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: unexpected error %v", i, errs[i])
		}
		if results[i] != "shared" {
			t.Fatalf("goroutine %d: want 'shared', got %q", i, results[i])
		}
	}
}

func TestDo_ContextCancelledWhileWaiting(t *testing.T) {
	var g Group
	leaderStarted := make(chan struct{})
	unblock := make(chan struct{})
	defer close(unblock)

	// Start a leader that blocks.
	go func() {
		g.Do(context.Background(), "k", func() ([]byte, error) { //nolint:errcheck
			close(leaderStarted)
			<-unblock
			return []byte("done"), nil
		})
	}()
	<-leaderStarted

	// Follower with already-cancelled context should get context.Canceled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := g.Do(ctx, "k", func() ([]byte, error) { return nil, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
