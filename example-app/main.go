package main

import (
	"log"
	"net/http"
	"os"

	"github.com/hossainshakhawat/distributed-cache/cache-client/client"
	"github.com/hossainshakhawat/distributed-cache/example-app/api"
	"github.com/hossainshakhawat/distributed-cache/example-app/db"
)

func main() {
	port := envOrDefault("PORT", "8888")
	routerAddr := envOrDefault("ROUTER_ADDR", "http://localhost:9000")
	invalidationAddr := envOrDefault("INVALIDATION_ADDR", "http://localhost:9100")

	cacheClient := client.New(client.Options{
		RouterAddr: routerAddr,
	})

	database := db.New()
	handler := api.New(cacheClient, database, invalidationAddr)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	addr := ":" + port
	log.Printf("example-app listening on %s (router=%s, invalidation=%s)", addr, routerAddr, invalidationAddr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("example-app: %v", err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
