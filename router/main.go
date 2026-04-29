package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/distributed-cache/router/server"
)

func main() {
	port := envOrDefault("PORT", "9000")
	nodesEnv := envOrDefault("CACHE_NODES", "http://localhost:8080,http://localhost:8081,http://localhost:8082")

	nodes := splitTrim(nodesEnv, ",")
	if len(nodes) == 0 {
		log.Fatal("router: no cache nodes configured (CACHE_NODES)")
	}

	router := server.New(nodes)

	mux := http.NewServeMux()
	router.RegisterRoutes(mux)

	addr := ":" + port
	log.Printf("router listening on %s, nodes: %v", addr, nodes)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("router: %v", err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
