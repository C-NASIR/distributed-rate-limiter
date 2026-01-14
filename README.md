# Ratelimit

A distributed rate limiting service that you can run as a standalone binary and integrate with any system through HTTP or gRPC.

This project is designed for engineers who need a production ready rate limiting control plane that supports multi tenancy, multiple algorithms, high throughput, graceful degradation, and strong operational safety.

It is not a toy example. It is a full architectural reference implementation.

---

## What this service gives you

1. Distributed rate limiting with stateless service nodes
2. Multiple algorithms per rule such as token bucket, fixed window, and sliding window
3. Hot rule updates with no restarts and fast convergence
4. Safe limiter cutover with quiesce windows
5. Batch rate limit evaluation for high throughput clients
6. Graceful degradation when Redis or membership becomes unhealthy
7. Ownership aware fallback limiting to avoid multiplied limits
8. Region aware behavior and quorum based degradation
9. Observability through metrics, tracing, and mode introspection
10. HTTP and gRPC APIs with identical semantics
11. Configuration through file, environment, and flags
12. Benchmarks and chaos testing harness

This project is intended to be used as:

A standalone rate limiting service  
A reference architecture for building similar systems  
A foundation you can extend with real Redis, Postgres, and PubSub clients

---

## Architecture overview

At a high level the system is composed of:

1. Rule control plane  
   Rules are stored in a database, published through an outbox, and propagated through pubsub invalidation. A full sync worker acts as a safety net.

2. Rule cache  
   Rules are stored in atomic snapshots for lock free reads on the hot path.

3. Limiter pool  
   Limiters are cached per tenant and resource, sharded for contention, with LRU eviction and clean cutover on rule changes.

4. Redis backed limiters  
   Token bucket, fixed window, and sliding window algorithms are implemented using Redis semantics. In this repo an in memory Redis is used for portability.

5. Fallback limiter  
   When Redis is unavailable the system falls back to a local limiter that is ownership aware and region scaled.

6. Degrade controller  
   The system automatically moves between normal, degraded, and emergency modes based on health signals.

7. Transports  
   HTTP and gRPC adapters that call transport agnostic services.

8. Observability  
   Metrics, tracing, health, readiness, and mode endpoints.

---

## Build

```sh
go test ./...
go test -bench=. ./internal/ratelimit
```

---

## Run with config file

```sh
go run ./cmd/ratelimit --config configs/local.json
```

---

## Run with env

Env values use milliseconds for time fields.

```sh
RATELIMIT_REGION=local \
RATELIMIT_ENABLE_HTTP=true \
RATELIMIT_HTTP_ADDR=:8080 \
RATELIMIT_ENABLE_GRPC=true \
RATELIMIT_GRPC_ADDR=:9090 \
RATELIMIT_TRACE_SAMPLE_RATE=100 \
RATELIMIT_COALESCE_TTL_MS=10 \
RATELIMIT_BREAKER_OPEN_MS=200 \
go run ./cmd/ratelimit
```

---

## Docker

```sh
docker build -t ratelimit .
docker run -p 8080:8080 -p 9090:9090 ratelimit
```

---

## API quick test

```sh
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

The HTTP API listens on port 8080 and gRPC listens on port 9090.

---

## HTTP API overview

```
Rate limit

POST /v1/ratelimit/check
POST /v1/ratelimit/checkBatch

Admin

POST /v1/admin/rules
PUT /v1/admin/rules
DELETE /v1/admin/rules
GET /v1/admin/rules
GET /v1/admin/rules/list

Operations

GET /health
GET /ready
GET /metrics
GET /mode

All responses are JSON. Rate limit endpoints always return HTTP 200 for handled requests and encode errors in the response body.
```

---

## gRPC API overview

```
The gRPC API mirrors the HTTP API exactly and uses the protobuf definitions in:

proto/ratelimit/v1/ratelimit.proto

Services:

RateLimitService
AdminService
HealthService

You can connect using any standard gRPC client.
```

---

## Operating modes

The system automatically operates in three modes:

Normal
Redis is healthy and ownership is respected

Degraded
Redis is unhealthy but membership is healthy

Emergency
Redis and membership are unhealthy or region quorum is not met

Fallback behavior and caps change based on the current mode.

You can inspect the current mode using the HTTP or gRPC mode endpoint.

---

## Configuration model

Configuration is loaded in this order:

1. Defaults in code
2. Config file
3. Environment variables
4. Command line flags

Later sources override earlier ones.

You can inspect the final merged configuration using:

```sh
go run ./cmd/ratelimit print_config
```

---

## Benchmarks

Benchmarks are provided for the hot path:

CheckLimit
CheckLimitBatch

Run them with:

```sh
go test -bench=. ./internal/ratelimit
```

These benchmarks are designed to reflect realistic in process performance characteristics.

---

## Chaos testing

A chaos runner is included to validate system behavior under failure conditions such as:

Redis outages
Rule update storms
Pubsub message loss

You can run the chaos runner through tests or extend it with your own scenarios.

---

## When to use this project

Use this project if you need:

A real distributed rate limiting service
A control plane for API gateways or internal services
A reference design for resilience and convergence
A system that is safe under failure

Do not use it as a simple library. It is designed as a service.

---

## Extending this project

To move toward production you would typically:

Replace in memory Redis with a real Redis client
Replace in memory RuleDB with Postgres or similar
Replace in memory PubSub with Redis or Kafka
Deploy multiple instances per region
Add authentication for rate limit APIs if needed

No architectural changes are required to do this.

---

## Design philosophy

This project favors:

Correctness over cleverness
Deterministic convergence
Explicit state transitions
Safe degradation
Clear ownership boundaries

Every major distributed systems failure mode is explicitly handled.

---

## Contribution

Contributions are welcome in the form of:

Algorithm improvements
Performance tuning
New transports
New storage backends
Operational tooling

Please keep changes aligned with the existing architectural principles.

---

## License

MIT

---

If you are evaluating this project as an engineer, the best place to start is:

internal/ratelimit/rulecache.go
internal/ratelimit/limiterpool.go
internal/ratelimit/handler.go
internal/ratelimit/mode.go

These files capture the core of the system.

---

If you have questions or want to adapt this design for your own system, this repository is intended to be a practical reference, not just an example.
