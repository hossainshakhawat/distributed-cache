# Distributed Caching System

A Redis/Memcached-like distributed cache layer written in Go. Built around consistent hashing, read-through / write-through caching against PostgreSQL, stampede protection at both the client and node level, TTL jitter, local hot-key caching, and event-driven invalidation — each concern in its own service.

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────────┐
│                        Client Services                           │
│                      (example-app, etc.)                         │
└───────────────────────────┬──────────────────────────────────────┘
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
                 │        Router        │  (router/)
                 │  • consistent hash   │
                 │  • 150 virtual nodes │
                 │  • replica fan-out   │
                 │  • POST /db/update   │  ← routes writes to primary node
                 └────┬──────┬──────┬──┘
                      │      │      │
           ┌──────────▼─┐ ┌──▼────┐ ┌▼──────────┐
           │ Cache Node │ │ Cache │ │ Cache     │  (cache-node/)
           │     1      │ │ Node  │ │ Node 3    │
           │            │ │   2   │ │           │
           │ • TTL/LRU  │ │       │ │ • TTL/LFU │
           │ • eviction │ │       │ │ • eviction│
           │ • stampede │ │       │ │ • stampede│
           │   guard    │ │       │ │   guard   │
           └─────┬──────┘ └───────┘ └───────────┘
                 │ (read-through on miss / write-through on update)
                 │
        ┌────────▼────────┐
        │   PostgreSQL     │  (source of truth)
        │  • users table   │
        │  • auto-migrate  │
        │  • seed on boot  │
        └─────────────────┘

                 ┌──────────────────────┐
                 │  Invalidation Service │  (invalidation-service/)
                 │  • POST /invalidate   │
                 │  • fan-out DELETE to  │
                 │    all cache-nodes    │
                 │  • event bus (→Kafka) │
                 └──────────────────────┘
```

---

## Services

| Service | Port | Responsibility |
|---|---|---|
| `cache-node` | 8080–8082 | Stores keys in memory. TTL, lazy expiry, background cleanup, LRU/LFU eviction. **Owns PostgreSQL** — read-through on miss, write-through on `/db/update`. Stampede protection (singleflight) for concurrent DB loads. |
| `router` | 9000 | Consistent hash ring. Routes GET/SET/DELETE to the correct node(s). Routes `POST /db/update` to the **primary** node for a key (single writer). Async replica fan-out for SET. |
| `cache-client` | — | Library (not a server). Used by app services for cache-aside reads, stampede protection (singleflight), hot-key local in-process cache, TTL jitter. |
| `invalidation-service` | 9100 | Accepts invalidation events via HTTP. Fans out DELETE to all cache-nodes so every replica evicts the stale key. |
| `example-app` | 8888 | Demo application. No database dependency. Reads via cache-client; writes via `POST router/db/update`. |
| `postgres` | 5432 | Source of truth. Schema auto-migrated and seeded by cache-node on startup. |

---

## Request Flows

### Read (read-through)

```
Client → GET /users/1
  │
  ├─► local in-process cache (5 s TTL)?
  │     hit  → return immediately
  │     miss ↓
  │
  ├─► stampede guard @ cache-client (singleflight)
  │     only ONE goroutine proceeds to router; others wait
  │     ↓
  ├─► router GET /get?key=user:1
  │     → consistent hash ring → Cache Node N
  │         hit  → return value                 ← X-Cache: HIT
  │         miss ↓
  │
  ├─► stampede guard @ cache-node (singleflight per key)
  │     only ONE goroutine queries DB; others wait for result
  │     ↓
  ├─► cache-node → SELECT FROM users WHERE id=1  (PostgreSQL)
  │     → store value (TTL 5 min)
  │
  └─► return value to client                    ← X-Cache: MISS
```

### Write (write-through + invalidation)

```
Client → PUT /users/1  {"name": "Alice"}
  │
  ├─► example-app → POST router/db/update  {"key":"user:1","name":"Alice"}
  │     → router: consistent hash → primary Cache Node N only
  │         → UPDATE users SET name='Alice' WHERE id=1 RETURNING ...  (PostgreSQL)
  │         → store updated value in node N's cache (TTL 5 min)
  │         → return updated JSON
  │
  └─► example-app → POST invalidation-service/invalidate  {"keys":["user:1"]}
        → worker fans out DELETE /delete?key=user:1
          to ALL cache-nodes (evicts stale copy from replicas)
```

### Consistent Hash Ring

```
key "user:123"  →  SHA-256  →  uint32 position
                               │
              ┌────────────────▼───────────────────┐
              │  Ring (sorted virtual node points)  │
              │  Node-A·vn0 ... Node-B·vn99 ...     │
              │  Node-C·vn149 ...                   │
              └─────────────────────────────────────┘
                               │
                    walk clockwise to first point ≥ hash
                               │
                         Cache Node selected
```

Adding/removing a node only remaps keys on that node's ring segment — not the entire keyspace.

### Stampede Protection (two layers)

```
Layer 1 — cache-client (client side)
─────────────────────────────────────
1000 app goroutines miss "user:1" simultaneously
         │
         ▼
  stampede.Group.Do("user:1", fn)  (cache-client/stampede)
         │
  ┌──────┴─────────────────────┐
  │  goroutine #1 → calls router
  │  goroutines #2-1000 → wait
  └──────┬─────────────────────┘
         │  result returned
         ▼
  all 1000 receive same value — router hit count: 1


Layer 2 — cache-node (server side)
─────────────────────────────────────
After router reaches cache-node, concurrent misses for same key:
         │
         ▼
  flightGroup.do("user:1", db.Load)  (cache-node/server)
         │
  ┌──────┴─────────────────────┐
  │  request #1 → SELECT FROM postgres
  │  requests #2-N → wait on WaitGroup
  └──────┬─────────────────────┘
         │  DB result shared
         ▼
  DB call count: 1  (not N)
```

---

## Running Locally

### Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- [Docker + Docker Compose](https://docs.docker.com/get-docker/)

---

### Option A — Docker Compose (recommended)

```bash
git clone https://github.com/hossainshakhawat/distributed-cache.git
cd distributed-cache

docker compose up --build
```

Services start in dependency order:
1. `postgres`
2. `cache-node-1`, `cache-node-2`, `cache-node-3` (connect to PostgreSQL; auto-migrate + seed)
3. `router`
4. `invalidation-service`
5. `example-app`

---

### Option B — Run Each Service Manually

Start PostgreSQL first (Docker is easiest):

```bash
docker run --rm -p 5432:5432 \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=appdb \
  postgres:16-alpine
```

Open **six** terminals.

**Terminal 1 — Cache Node 1** (primary DB node; auto-migrates and seeds)
```bash
cd cache-node
PORT=8080 MAX_KEYS=50000 \
DATABASE_URL="postgres://postgres:postgres@localhost:5432/appdb?sslmode=disable" \
go run .
```

**Terminal 2 — Cache Node 2**
```bash
cd cache-node
PORT=8081 MAX_KEYS=50000 \
DATABASE_URL="postgres://postgres:postgres@localhost:5432/appdb?sslmode=disable" \
go run .
```

**Terminal 3 — Cache Node 3**
```bash
cd cache-node
PORT=8082 MAX_KEYS=50000 \
DATABASE_URL="postgres://postgres:postgres@localhost:5432/appdb?sslmode=disable" \
go run .
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

### 1. Get a user (first call → cache MISS, read-through from PostgreSQL)

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

### 3. Update a user (write-through to PostgreSQL + cache refresh + invalidation)

```bash
curl -i -X PUT http://localhost:8888/users/1 \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice"}'
```

```
HTTP/1.1 200 OK
{"id":1,"name":"Alice","updated_at":...}
```

### 4. Next GET is an immediate HIT with the updated value

```bash
curl -i http://localhost:8888/users/1
```

```
HTTP/1.1 200 OK
X-Cache: HIT          ← primary node refreshed the cache on write
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
| `EVICTION_POLICY` | `lru` | `lru` or `lfu` |
| `DATABASE_URL` | _(none)_ | PostgreSQL DSN. When set: read-through on miss, write-through on `/db/update`, auto-migrate + seed on startup. |

### router

| Variable | Default | Description |
|---|---|---|
| `PORT` | `9000` | HTTP listen port |
| `CACHE_NODES` | `http://localhost:8080,...` | Comma-separated cache-node addresses |

### invalidation-service

| Variable | Default | Description |
|---|---|---|
| `PORT` | `9100` | HTTP listen port |
| `ROUTER_ADDR` | `http://localhost:9000` | Router address |

### example-app

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8888` | HTTP listen port |
| `ROUTER_ADDR` | `http://localhost:9000` | Router address (also used for `/db/update` writes) |
| `INVALIDATION_ADDR` | `http://localhost:9100` | Invalidation service address |

---

## Configuration Reference (cache-client library)

```go
client.New(client.Options{
    RouterAddr:     "http://router:9000",
    DefaultTTL:     5 * time.Minute,        // base TTL
    TTLJitter:      30 * time.Second,        // random jitter to avoid synchronised expiry
    LocalCacheSize: 512,                     // in-process hot-key entries
    LocalCacheTTL:  5 * time.Second,         // local cache lifetime
    HTTPTimeout:    500 * time.Millisecond,
})
```

---

## Design Decisions

### Why does cache-node own PostgreSQL (not example-app)?

The cache layer is the single point of contact for all data reads and writes. Keeping the DB connection in cache-node means:
- Any service can read/write through the cache without its own DB dependency.
- Write-through is enforced at the infrastructure level, not per-service.
- `example-app` becomes stateless and DB-free, making it easy to replace or scale.

### Why write-through instead of delete on write?

`POST /db/update` writes to PostgreSQL and immediately refreshes the primary node's cache. The updated value is available on the very next GET without a DB round-trip. Stale replicas are evicted asynchronously via the invalidation service.

### Why route `/db/update` to the primary node only?

Writes must be serialised per key to avoid race conditions. The consistent hash ring deterministically maps a key to one primary node, so concurrent updates for the same key are naturally serialised at that node.

### Why async replica writes?

The cache is not the source of truth. Losing a recent write to a replica is acceptable — the next read will rebuild it from PostgreSQL. Async replication keeps write latency low.

### Why two layers of stampede protection?

- **cache-client layer** collapses redundant router calls from the same process (many app goroutines → one network request).
- **cache-node layer** collapses redundant DB queries when multiple nodes or processes miss the same key simultaneously (many router requests → one DB query).

### Why TTL jitter?

If many keys are set at the same time with the same TTL (e.g., after a deployment flush), they all expire together and cause a mass stampede. Adding `rand(0..TTLJitter)` spreads expiry over time.

### Why virtual nodes in the hash ring?

Without virtual nodes a node with fewer physical positions receives less traffic. 150 virtual nodes per physical node gives an even distribution and ensures only a small fraction of keys move when a node is added or removed.

---

## Project Layout

```
distributed-cache/
├── Dockerfile                        # Multi-stage build; ARG SERVICE selects which binary
├── docker-compose.yml                # postgres + 3 cache-nodes + router + invalidation + example-app
├── go.work                           # Go workspace linking all modules
│
├── cache-node/                       # Stateful cache node + DB owner
│   ├── db/db.go                      # PostgreSQL: Load (read-through), Update (write-through), migrate+seed
│   ├── store/store.go                # In-memory KV: TTL, lazy expiry, LRU/LFU eviction, background GC
│   ├── server/server.go              # HTTP: /get /set /delete /db/update /health; singleflight stampede guard
│   └── main.go
│
├── router/                           # Consistent hash router
│   ├── hashring/hashring.go          # SHA-256 ring, 150 virtual nodes, AddNode/RemoveNode
│   ├── proxy/node_client.go          # HTTP client per cache-node (Get/Set/Delete/Update)
│   ├── server/server.go              # Routes requests; /db/update → primary node; async replica SET
│   └── main.go
│
├── cache-client/                     # Reusable library (not a server)
│   ├── client/client.go              # Cache-aside, TTL jitter, fail-open, local cache integration
│   ├── localcache/local.go           # Fixed-capacity in-process cache (hot keys)
│   └── stampede/group.go             # Singleflight implementation (client-side stampede guard)
│
├── invalidation-service/             # Event-driven cache invalidation
│   ├── queue/bus.go                  # In-process pub/sub (swap for Kafka in production)
│   ├── invalidator/worker.go         # Consumes events → DELETE via router to all nodes
│   ├── server/server.go              # POST /invalidate
│   └── main.go
│
└── example-app/                      # Demo service (no DB dependency)
    ├── api/api.go                    # GET /users/:id  PUT /users/:id (writes via router /db/update)
    └── main.go
```

