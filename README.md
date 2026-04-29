# Distributed Caching System

A Redis/Memcached-like distributed cache layer written in Go. Built around consistent hashing, replication, stampede protection, TTL jitter, local hot-key caching, and event-driven invalidation — each concern in its own service.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        Client Services                          │
│                      (example-app, etc.)                        │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                    ┌──────────▼──────────┐
                    │   Cache Client Lib   │  (cache-client/)
                    │  • local hot-key     │
                    │  • stampede guard    │
                    │  • TTL jitter        │
                    │  • fail-open         │
                    └──────────┬──────────┘
                               │ HTTP
                    ┌──────────▼──────────┐
                    │       Router         │  (router/)
                    │  • consistent hash   │
                    │  • 150 virtual nodes │
                    │  • replica fan-out   │
                    └───┬──────┬──────┬───┘
                        │      │      │
              ┌─────────▼─┐ ┌──▼────┐ ┌▼─────────┐
              │ Cache Node│ │ Cache │ │ Cache    │  (cache-node/)
              │     1     │ │ Node  │ │ Node 3   │
              │           │ │   2   │ │          │
              │ • TTL     │ │       │ │ • LFU    │
              │ • LFU     │ │       │ │ • expiry │
              │ • eviction│ │       │ │ • cleanup│
              └───────────┘ └───────┘ └──────────┘

                    ┌──────────────────────┐
                    │  Invalidation Service │  (invalidation-service/)
                    │  • POST /invalidate   │
                    │  • fan-out to router  │
                    │  • event bus (→Kafka) │
                    └──────────────────────┘
```

---

## Services

| Service | Port | Responsibility |
|---|---|---|
| `cache-node` | 8080–8082 | Stores keys in memory. Handles TTL, lazy expiry, background cleanup, LFU eviction. |
| `router` | 9000 | Consistent hash ring. Routes GET/SET/DELETE to the correct node(s). Writes replicas async. |
| `cache-client` | — | Library (not a server). Used by app services for cache-aside reads, stampede protection, hot-key local cache. |
| `invalidation-service` | 9100 | Accepts invalidation events via HTTP, deletes keys from the cache cluster. |
| `example-app` | 8888 | Demo application. Shows cache-aside GET, write-then-invalidate PUT. |

---

## Request Flows

### Read (cache-aside)

```
Client → GET /users/1
  │
  ├─► local in-process cache (5 s TTL)?
  │     hit  → return immediately
  │     miss ↓
  │
  ├─► stampede guard (singleflight)
  │     only ONE goroutine proceeds; others wait for its result
  │     ↓
  ├─► router GET /get?key=user:1
  │     → consistent hash ring → Cache Node N
  │         hit  → return value
  │         miss ↓
  │
  ├─► Loader (DB query, ~10 ms simulated)
  │
  ├─► router SET /set  (key, value, TTL + jitter)
  │     → primary node (sync)
  │     → replica node (async)
  │
  └─► return value to client
        X-Cache: HIT | MISS
```

### Write (update DB → delete cache)

```
Client → PUT /users/1  {"name": "Alice"}
  │
  ├─► DB update  (source of truth)
  │
  ├─► router DELETE /delete?key=user:1
  │     → deletes from primary + replica
  │
  └─► POST invalidation-service /invalidate  {"keys":["user:1"], "source":"example-app"}
        → event bus → worker → router DELETE
          (notifies other services that may hold this key)
```

### Consistent Hash Ring

```
key "user:123"  →  SHA-256  →  uint32 position
                               │
              ┌────────────────▼───────────────────┐
              │  Ring (sorted virtual node points)  │
              │  Node-A·vn0 ... Node-B·vn99 ...     │
              │  Node-C·vn149 ...                   │
              └────────────────────────────────────-┘
                               │
                    walk clockwise to first point ≥ hash
                               │
                         Cache Node selected
```

Adding/removing a node only remaps the keys that were on that node's segment — not the entire keyspace.

### Stampede Protection

```
1000 goroutines miss "user:123" at the same moment
         │
         ▼
  singleflight.Group.Do("user:123", loader)
         │
  ┌──────┴──────────────────────┐
  │  goroutine #1  →  runs loader (DB + cache SET)
  │  goroutines #2-1000 → wait on WaitGroup
  └──────┬──────────────────────┘
         │  loader done
         ▼
  all 1000 goroutines receive the same []byte result
  DB hit count: 1  (not 1000)
```

---

## Running Locally

### Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- [Docker + Docker Compose](https://docs.docker.com/get-docker/) (for containerised run)

---

### Option A — Docker Compose (recommended)

```bash
git clone https://github.com/hossainshakhawat/distributed-cache.git
cd distributed-cache

docker compose up --build
```

Services start in dependency order:
1. `cache-node-1`, `cache-node-2`, `cache-node-3`
2. `router` (waits for nodes)
3. `invalidation-service` (waits for router)
4. `example-app` (waits for router + invalidation-service)

---

### Option B — Run Each Service Manually

Open **five** terminals.

**Terminal 1 — Cache Node 1**
```bash
cd cache-node
PORT=8080 MAX_KEYS=50000 go run .
```

**Terminal 2 — Cache Node 2**
```bash
cd cache-node
PORT=8081 MAX_KEYS=50000 go run .
```

**Terminal 3 — Cache Node 3**
```bash
cd cache-node
PORT=8082 MAX_KEYS=50000 go run .
```

**Terminal 4 — Router**
```bash
cd router
PORT=9000 \
CACHE_NODES="http://localhost:8080,http://localhost:8081,http://localhost:8082" \
go run .
```

**Terminal 5 — Invalidation Service**
```bash
cd invalidation-service
PORT=9100 ROUTER_ADDR="http://localhost:9000" go run .
```

**Terminal 6 — Example App**
```bash
cd example-app
PORT=8888 \
ROUTER_ADDR="http://localhost:9000" \
INVALIDATION_ADDR="http://localhost:9100" \
go run .
```

---

## Try It Out

### 1. Get a user (first call → cache MISS, loads from DB)

```bash
curl -i http://localhost:8888/users/1
```

```
HTTP/1.1 200 OK
X-Cache: MISS
{"id":1,"name":"User 1","updated_at":...}
```

### 2. Get the same user again (cache HIT)

```bash
curl -i http://localhost:8888/users/1
```

```
HTTP/1.1 200 OK
X-Cache: HIT
{"id":1,"name":"User 1","updated_at":...}
```

### 3. Update a user (DB write → cache invalidation)

```bash
curl -i -X PUT http://localhost:8888/users/1 \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice"}'
```

```
HTTP/1.1 200 OK
{"id":1,"name":"Alice","updated_at":...}
```

### 4. Next GET reloads fresh value from DB

```bash
curl -i http://localhost:8888/users/1
```

```
HTTP/1.1 200 OK
X-Cache: MISS           ← key was deleted; fresh DB load
{"id":1,"name":"Alice","updated_at":...}
```

### 5. Trigger invalidation from any external service

```bash
curl -X POST http://localhost:9100/invalidate \
  -H "Content-Type: application/json" \
  -d '{"keys":["user:1","user:2"],"source":"my-other-service"}'
```

```
HTTP/1.1 202 Accepted
```

### 6. Health checks

```bash
curl http://localhost:8080/health   # cache-node-1
curl http://localhost:9000/health   # router
curl http://localhost:9000/nodes    # list ring members
curl http://localhost:9100/health   # invalidation-service
curl http://localhost:8888/health   # example-app
```

---

## Environment Variables

### cache-node

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `MAX_KEYS` | `100000` | Max keys before eviction triggers |

### router

| Variable | Default | Description |
|---|---|---|
| `PORT` | `9000` | HTTP listen port |
| `CACHE_NODES` | `http://localhost:8080,...` | Comma-separated list of cache-node addresses |

### invalidation-service

| Variable | Default | Description |
|---|---|---|
| `PORT` | `9100` | HTTP listen port |
| `ROUTER_ADDR` | `http://localhost:9000` | Router address |

### example-app

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8888` | HTTP listen port |
| `ROUTER_ADDR` | `http://localhost:9000` | Router address |
| `INVALIDATION_ADDR` | `http://localhost:9100` | Invalidation service address |

---

## Configuration Reference (cache-client library)

```go
client.New(client.Options{
    RouterAddr:     "http://router:9000",
    DefaultTTL:     5 * time.Minute,   // base TTL
    TTLJitter:      30 * time.Second,  // random jitter added to avoid synchronized expiry
    LocalCacheSize: 512,               // in-process hot-key entries
    LocalCacheTTL:  5 * time.Second,   // local cache lifetime
    HTTPTimeout:    500 * time.Millisecond,
})
```

---

## Design Decisions

### Why delete on write instead of update?

Updating the cache on every write risks pushing stale derived data. Deleting forces the next reader to reload the freshest value from the DB. This is simpler and avoids consistency bugs.

### Why async replica writes?

The cache is not the source of truth. Losing a recent write to a replica is acceptable — the next read will rebuild it from the DB. Async replication keeps write latency low.

### Why singleflight for stampede protection?

When a hot key expires, thousands of concurrent misses would all hit the DB. Singleflight collapses all concurrent fetches for the same key into one DB call; the result is shared with all waiters.

### Why TTL jitter?

If many keys are set at the same time with the same TTL (e.g., after a deployment flush), they all expire together and cause a mass stampede. Adding `rand(0..TTLJitter)` spreads expiry across time.

### Why virtual nodes in the hash ring?

Without virtual nodes a node with fewer physical slots would receive less traffic. 150 virtual nodes per physical node gives an even distribution and ensures only a small fraction of keys move when a node is added or removed.

---

## Project Layout

```
distributed-cache/
├── Dockerfile                        # Multi-stage build; ARG SERVICE selects which binary
├── docker-compose.yml
├── go.work                           # Go workspace linking all modules
│
├── cache-node/                       # Stateful cache node
│   ├── store/store.go                # In-memory store: TTL, lazy expiry, LFU eviction, background GC
│   ├── server/server.go              # HTTP: GET /get  POST /set  DELETE /delete  GET /health
│   └── main.go
│
├── router/                           # Consistent hash router
│   ├── hashring/hashring.go          # SHA-256 ring, 150 virtual nodes, AddNode/RemoveNode
│   ├── proxy/node_client.go          # HTTP client per cache-node
│   ├── server/server.go              # Routes requests; async replica writes
│   └── main.go
│
├── cache-client/                     # Reusable library (not a server)
│   ├── client/client.go              # Cache-aside, TTL jitter, fail-open, local cache integration
│   ├── localcache/local.go           # Fixed-capacity in-process cache (hot keys)
│   └── stampede/group.go            # Singleflight implementation
│
├── invalidation-service/             # Event-driven cache invalidation
│   ├── queue/bus.go                  # In-process pub/sub (swap for Kafka in production)
│   ├── invalidator/worker.go         # Consumes events → DELETE via router
│   ├── server/server.go              # POST /invalidate
│   └── main.go
│
└── example-app/                      # Demo service
    ├── db/db.go                      # Simulated database (10 ms read latency)
    ├── api/api.go                    # GET /users/:id  PUT /users/:id
    └── main.go
```
