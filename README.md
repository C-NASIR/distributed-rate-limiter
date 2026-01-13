# ratelimit

Distributed rate limiting service skeleton (phase 0).

## Build

```sh
go test ./...
go test -bench=. ./internal/ratelimit
```

## Run with config file

```sh
go run ./cmd/ratelimit --config configs/local.json
```

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

## Docker

```sh
docker build -t ratelimit .
docker run -p 8080:8080 -p 9090:9090 ratelimit
```

## API quick test

```sh
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

The HTTP API listens on port 8080 and gRPC listens on port 9090.
