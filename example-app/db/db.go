// Package db simulates a database / source of truth.
package db

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// User is the data model.
type User struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updated_at"`
}

// DB is an in-memory pseudo-database.
type DB struct {
	mu    sync.RWMutex
	users map[int]*User
}

// New creates a DB pre-seeded with test data.
func New() *DB {
	d := &DB{users: make(map[int]*User)}
	for i := 1; i <= 10; i++ {
		d.users[i] = &User{ID: i, Name: fmt.Sprintf("User %d", i), UpdatedAt: time.Now().UnixNano()}
	}
	return d
}

// GetUser simulates a DB read (slow).
func (d *DB) GetUser(_ context.Context, id int) (*User, error) {
	time.Sleep(10 * time.Millisecond) // simulate DB latency
	d.mu.RLock()
	defer d.mu.RUnlock()
	u, ok := d.users[id]
	if !ok {
		return nil, fmt.Errorf("user %d not found", id)
	}
	return u, nil
}

// UpdateUser simulates a DB write.
func (d *DB) UpdateUser(_ context.Context, id int, name string) (*User, error) {
	time.Sleep(5 * time.Millisecond)
	d.mu.Lock()
	defer d.mu.Unlock()
	u, ok := d.users[id]
	if !ok {
		return nil, fmt.Errorf("user %d not found", id)
	}
	u.Name = name
	u.UpdatedAt = time.Now().UnixNano()
	return u, nil
}
