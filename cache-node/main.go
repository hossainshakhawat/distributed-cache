package main

import (
	"log"
	"net/http"
	"os"
	"strconv"

	cachedb "github.com/hossainshakhawat/distributed-cache/cache-node/db"
	"github.com/hossainshakhawat/distributed-cache/cache-node/server"
	"github.com/hossainshakhawat/distributed-cache/cache-node/store"
)

func main() {
	port := envOrDefault("PORT", "8080")
	maxKeys := envIntOrDefault("MAX_KEYS", 100_000)
	policy := store.PolicyLFU

	var db *cachedb.DB
	if connStr := envOrDefault("DATABASE_URL", ""); connStr != "" {
		d, err := cachedb.New(connStr)
		if err != nil {
			log.Fatalf("cache-node: db: %v", err)
		}
		db = d
	}

	s := store.New(maxKeys, policy)
	srv := server.New(s, db)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	addr := ":" + port
	log.Printf("cache-node listening on %s (maxKeys=%d)", addr, maxKeys)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("cache-node: %v", err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return def
}
