package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/distributed-cache/invalidation-service/invalidator"
	"github.com/distributed-cache/invalidation-service/queue"
	"github.com/distributed-cache/invalidation-service/server"
)

func main() {
	port := envOrDefault("PORT", "9100")
	routerAddr := envOrDefault("ROUTER_ADDR", "http://localhost:9000")

	bus := queue.NewBus()
	worker := invalidator.New(routerAddr, bus)
	srv := server.New(bus)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start worker goroutine.
	go worker.Run(ctx)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	httpServer := &http.Server{Addr: ":" + port, Handler: mux}

	go func() {
		<-ctx.Done()
		log.Println("invalidation-service: shutting down")
		bus.DrainAndClose(context.Background())
		_ = httpServer.Shutdown(context.Background())
	}()

	log.Printf("invalidation-service listening on :%s, router=%s", port, routerAddr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("invalidation-service: %v", err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
