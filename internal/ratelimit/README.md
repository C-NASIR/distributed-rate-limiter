# internal/ratelimit

This directory is split into focused subpackages:

- `app`: application wiring, integration helpers, and benchmarks/chaos.
- `config`: configuration structs and loading helpers.
- `core`: limiter interfaces, models, handlers, caches, and core logic.
- `observability`: logging, tracing, and metrics helpers.
- `store/inmemory`: in-memory data stores, outbox, pubsub, and Redis stand-ins.
- `transport/http`: HTTP transport implementation.
- `transport/grpc`: gRPC transport implementation.

Use the `app` package as the main entry point for wiring the service.
