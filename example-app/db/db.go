// Package db provides the source-of-truth database layer.
// Production uses PostgreSQL; tests use the InMemoryDB implementation.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// User is the data model.
type User struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updated_at"`
}

// Store is the interface that the API layer depends on.
type Store interface {
	GetUser(ctx context.Context, id int) (*User, error)
	UpdateUser(ctx context.Context, id int, name string) (*User, error)
}

// ── PostgreSQL implementation ─────────────────────────────────────────────────

// DB wraps a PostgreSQL connection pool.
type DB struct {
	conn *sql.DB
}

// New opens a PostgreSQL connection, applies the schema, and seeds initial rows.
// connStr example: "postgres://user:pass@host:5432/dbname?sslmode=disable"
func New(connStr string) (*DB, error) {
	conn, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("db: migrate: %w", err)
	}
	if err := d.seed(); err != nil {
		return nil, fmt.Errorf("db: seed: %w", err)
	}
	return d, nil
}

// migrate creates the users table if it does not already exist.
func (d *DB) migrate() error {
	_, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id         SERIAL PRIMARY KEY,
			name       TEXT   NOT NULL,
			updated_at BIGINT NOT NULL DEFAULT 0
		)`)
	return err
}

// seed inserts 10 default users, ignoring conflicts so restarts are idempotent.
func (d *DB) seed() error {
	for i := 1; i <= 10; i++ {
		if _, err := d.conn.Exec(`
			INSERT INTO users (id, name, updated_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (id) DO NOTHING`,
			i, fmt.Sprintf("User %d", i), time.Now().UnixNano(),
		); err != nil {
			return err
		}
	}
	// Keep the sequence in sync after explicit ID inserts.
	_, err := d.conn.Exec(`SELECT setval('users_id_seq', (SELECT MAX(id) FROM users))`)
	return err
}

// GetUser fetches a user by ID from PostgreSQL.
func (d *DB) GetUser(ctx context.Context, id int) (*User, error) {
	row := d.conn.QueryRowContext(ctx,
		`SELECT id, name, updated_at FROM users WHERE id = $1`, id)
	u := &User{}
	if err := row.Scan(&u.ID, &u.Name, &u.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user %d not found", id)
		}
		return nil, err
	}
	return u, nil
}

// UpdateUser updates the user's name in PostgreSQL and returns the updated record.
func (d *DB) UpdateUser(ctx context.Context, id int, name string) (*User, error) {
	updatedAt := time.Now().UnixNano()
	row := d.conn.QueryRowContext(ctx,
		`UPDATE users SET name = $1, updated_at = $2
		 WHERE id = $3
		 RETURNING id, name, updated_at`, name, updatedAt, id)
	u := &User{}
	if err := row.Scan(&u.ID, &u.Name, &u.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user %d not found", id)
		}
		return nil, err
	}
	return u, nil
}

// ── In-memory implementation (unit tests) ────────────────────────────────────

// InMemoryDB implements Store with a plain map. Used in unit tests.
type InMemoryDB struct {
	mu    sync.RWMutex
	users map[int]*User
}

// NewInMemory returns an InMemoryDB pre-seeded with 10 users.
func NewInMemory() *InMemoryDB {
	d := &InMemoryDB{users: make(map[int]*User)}
	for i := 1; i <= 10; i++ {
		d.users[i] = &User{ID: i, Name: fmt.Sprintf("User %d", i), UpdatedAt: time.Now().UnixNano()}
	}
	return d
}

// GetUser looks up a user in the in-memory map.
func (d *InMemoryDB) GetUser(_ context.Context, id int) (*User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	u, ok := d.users[id]
	if !ok {
		return nil, fmt.Errorf("user %d not found", id)
	}
	return u, nil
}

// UpdateUser updates a user's name in the in-memory map.
func (d *InMemoryDB) UpdateUser(_ context.Context, id int, name string) (*User, error) {
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
