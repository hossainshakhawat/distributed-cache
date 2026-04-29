// Package db provides a read-only PostgreSQL loader for the cache-node.
// On a cache miss the server calls Load(key); the result is stored and
// returned as a cache hit so the client never needs a loader closure.
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

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
