package db

import (
	"context"
	"os"
	"testing"
)

// ── InMemoryDB unit tests (always run) ───────────────────────────────────────

func TestInMemory_Seeded(t *testing.T) {
	d := NewInMemory()
	for i := 1; i <= 10; i++ {
		if _, err := d.GetUser(context.Background(), i); err != nil {
			t.Fatalf("user %d not seeded: %v", i, err)
		}
	}
}

func TestInMemory_GetUser_Found(t *testing.T) {
	d := NewInMemory()
	u, err := d.GetUser(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 {
		t.Fatalf("want ID=1, got %d", u.ID)
	}
	if u.Name == "" {
		t.Fatal("expected non-empty name")
	}
}

func TestInMemory_GetUser_NotFound(t *testing.T) {
	d := NewInMemory()
	_, err := d.GetUser(context.Background(), 9999)
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}
}

func TestInMemory_UpdateUser_Success(t *testing.T) {
	d := NewInMemory()
	updated, err := d.UpdateUser(context.Background(), 1, "NewName")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "NewName" {
		t.Fatalf("want 'NewName', got %q", updated.Name)
	}
	if updated.UpdatedAt == 0 {
		t.Fatal("expected UpdatedAt to be set")
	}
	u, err := d.GetUser(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "NewName" {
		t.Fatalf("update not persisted: got %q", u.Name)
	}
}

func TestInMemory_UpdateUser_NotFound(t *testing.T) {
	d := NewInMemory()
	_, err := d.UpdateUser(context.Background(), 9999, "X")
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}
}

// ── PostgreSQL integration tests (skipped without DATABASE_URL) ───────────────

func TestPostgres_CRUD(t *testing.T) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		t.Skip("DATABASE_URL not set; skipping PostgreSQL integration test")
	}
	d, err := New(connStr)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	u, err := d.GetUser(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u.ID != 1 {
		t.Fatalf("want ID=1, got %d", u.ID)
	}

	updated, err := d.UpdateUser(context.Background(), 1, "PG Updated")
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.Name != "PG Updated" {
		t.Fatalf("want 'PG Updated', got %q", updated.Name)
	}

	_, err = d.GetUser(context.Background(), 9999)
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}
}
