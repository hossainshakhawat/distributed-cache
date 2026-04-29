package hashring

import (
	"testing"
)

func TestGetNode_EmptyRing(t *testing.T) {
	r := New(10)
	if got := r.GetNode("any"); got != "" {
		t.Fatalf("expected '' from empty ring, got %q", got)
	}
}

func TestGetNode_SingleNode(t *testing.T) {
	r := New(10)
	r.AddNode("node-a")
	if got := r.GetNode("some-key"); got != "node-a" {
		t.Fatalf("expected 'node-a', got %q", got)
	}
}

func TestGetNode_ConsistentRouting(t *testing.T) {
	r := New(100)
	r.AddNode("node-a")
	r.AddNode("node-b")
	r.AddNode("node-c")

	first := r.GetNode("user:42")
	for i := 0; i < 100; i++ {
		if got := r.GetNode("user:42"); got != first {
			t.Fatalf("inconsistent routing on iteration %d: got %q, want %q", i, got, first)
		}
	}
}

func TestRemoveNode(t *testing.T) {
	r := New(100)
	r.AddNode("node-a")
	r.AddNode("node-b")
	r.RemoveNode("node-a")

	nodes := r.Nodes()
	if len(nodes) != 1 || nodes[0] != "node-b" {
		t.Fatalf("expected only node-b after removal, got %v", nodes)
	}
	// Every key must route to node-b after node-a is removed.
	for _, key := range []string{"k1", "k2", "k3", "user:42", "product:7"} {
		if got := r.GetNode(key); got != "node-b" {
			t.Fatalf("key %q routed to %q after removing node-a", key, got)
		}
	}
}

func TestGetNodes_ReturnsDistinctNodes(t *testing.T) {
	r := New(100)
	r.AddNode("node-a")
	r.AddNode("node-b")
	r.AddNode("node-c")

	nodes := r.GetNodes("replicated-key", 2)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d: %v", len(nodes), nodes)
	}
	if nodes[0] == nodes[1] {
		t.Fatalf("expected distinct nodes, got %q twice", nodes[0])
	}
}

func TestGetNodes_FewerNodesThanRequested(t *testing.T) {
	r := New(100)
	r.AddNode("only-node")

	nodes := r.GetNodes("k", 5)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node (ring has 1), got %d", len(nodes))
	}
}

func TestGetNodes_EmptyRing(t *testing.T) {
	r := New(10)
	nodes := r.GetNodes("k", 2)
	if len(nodes) != 0 {
		t.Fatalf("expected 0 nodes from empty ring, got %d", len(nodes))
	}
}

func TestGetNodes_Zero(t *testing.T) {
	r := New(100)
	r.AddNode("node-a")
	nodes := r.GetNodes("k", 0)
	if len(nodes) != 0 {
		t.Fatalf("expected 0 nodes when n=0, got %d", len(nodes))
	}
}

func TestNodes_Distinct(t *testing.T) {
	r := New(50)
	r.AddNode("node-x")
	r.AddNode("node-y")
	r.AddNode("node-z")

	nodes := r.Nodes()
	seen := make(map[string]bool)
	for _, n := range nodes {
		if seen[n] {
			t.Fatalf("duplicate node %q in Nodes() output", n)
		}
		seen[n] = true
	}
	if !seen["node-x"] || !seen["node-y"] || !seen["node-z"] {
		t.Fatalf("expected all three nodes, got %v", nodes)
	}
}

func TestNew_DefaultVirtualNodes(t *testing.T) {
	r := New(0) // should default to 150
	r.AddNode("n")
	// 150 virtual nodes added; ring should be non-empty.
	if r.GetNode("k") != "n" {
		t.Fatal("expected ring to work with default virtual nodes")
	}
}
