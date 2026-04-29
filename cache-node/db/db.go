// Package db provides PostgreSQL read/write access for the cache-node.
// On a cache miss the server calls Load(key) to fill the cache.
// On a PUT the server calls Update(key, name) to persist the change and refresh the cache.
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// user mirrors the application data model for JSON serialisation.
type user struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updated_at"`
}

// DB holds a read-only PostgreSQL connection pool.
type DB struct {
	conn *sql.DB
}

// New opens and pings a PostgreSQL connection.
func New(connStr string) (*DB, error) {
	conn, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("cache-node/db: open: %w", err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("cache-node/db: ping: %w", err)
	}
	return &DB{conn: conn}, nil
}

// Load fetches the value for a cache key directly from PostgreSQL.
// Returns (nil, nil) when the key is not found or the key format is not
// recognised — the cache-node treats this as a miss rather than an error.
//
// Supported key formats:
//
//	user:{id}  →  SELECT id, name, updated_at FROM users WHERE id = $1
func (d *DB) Load(key string) ([]byte, error) {
	idStr, ok := strings.CutPrefix(key, "user:")
	if !ok {
		return nil, nil // unrecognised key prefix — pass through as miss
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return nil, fmt.Errorf("cache-node/db: bad key %q: %w", key, err)
	}
	u := &user{}
	err = d.conn.QueryRow(
		`SELECT id, name, updated_at FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Name, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // not found → caller treats it as a miss
	}
	if err != nil {
		return nil, fmt.Errorf("cache-node/db: load %q: %w", key, err)
	}
	return json.Marshal(u)
}

// Update updates the user's name for the given key in PostgreSQL and returns
// the updated record as JSON.
// Returns (nil, nil) when the key format is not recognised or the user is not found.
func (d *DB) Update(key, name string) ([]byte, error) {
	idStr, ok := strings.CutPrefix(key, "user:")
	if !ok {
		return nil, nil
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return nil, fmt.Errorf("cache-node/db: bad key %q: %w", key, err)
	}
	u := &user{}
	err = d.conn.QueryRow(
		`UPDATE users SET name = $1, updated_at = $2
		 WHERE id = $3
		 RETURNING id, name, updated_at`,
		name, time.Now().UnixNano(), id,
	).Scan(&u.ID, &u.Name, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // not found
	}
	if err != nil {
		return nil, fmt.Errorf("cache-node/db: update %q: %w", key, err)
	}
	return json.Marshal(u)
}
