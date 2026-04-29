package db

import (
	"context"
	"testing"
)

func TestNew_Seeded(t *testing.T) {
	d := New()
	for i := 1; i <= 10; i++ {
		if _, err := d.GetUser(context.Background(), i); err != nil {
			t.Fatalf("user %d not seeded: %v", i, err)
		}
	}
}

func TestGetUser_Found(t *testing.T) {
	d := New()
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

func TestGetUser_NotFound(t *testing.T) {
	d := New()
	_, err := d.GetUser(context.Background(), 9999)
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}
}

func TestUpdateUser_Success(t *testing.T) {
	d := New()
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
	// Verify the update persists.
	u, err := d.GetUser(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "NewName" {
		t.Fatalf("update not persisted: got %q", u.Name)
	}
}

func TestUpdateUser_NotFound(t *testing.T) {
	d := New()
	_, err := d.UpdateUser(context.Background(), 9999, "X")
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}
}
