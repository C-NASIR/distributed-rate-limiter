# Rate Limit Package Refactor Plan

## Goals
- Split `internal/ratelimit` into focused subpackages by responsibility.
- Keep public APIs stable while moving internal types/implementations.
- Make it obvious where new components should live.

## Proposed Package Layout
- `internal/ratelimit/core` — primary limiter interfaces and orchestration.
- `internal/ratelimit/algorithms` — token bucket, leaky bucket, sliding window, etc.
- `internal/ratelimit/store` — storage interface + shared store helpers.
- `internal/ratelimit/store/<backend>` — backend implementations (e.g., redis, in-memory).
- `internal/ratelimit/transport/http` — HTTP handlers, middleware, routing.
- `internal/ratelimit/transport/grpc` — gRPC handlers, interceptors.
- `internal/ratelimit/config` — config structs, parsing, validation.
- `internal/ratelimit/metrics` — metrics interfaces and exporters.
- `internal/ratelimit/logging` — logging helpers/adapters.
- `internal/ratelimit/time` — clock abstractions, time helpers.
- `internal/ratelimit/types` — shared types, errors, constants.

## File-to-Package Routing Rules
- API-facing limiter interfaces, orchestrators -> `core`.
- Algorithm implementations -> `algorithms`.
- Store interfaces + shared store helpers -> `store`.
- Backend-specific stores -> `store/<backend>` (one package per backend).
- HTTP handlers, middleware, route wiring -> `transport/http`.
- gRPC services/interceptors -> `transport/grpc`.
- Config structs, defaults, env parsing -> `config`.
- Metrics interfaces and adapters -> `metrics`.
- Logging wrappers/adapters -> `logging`.
- Clock/time abstractions -> `time`.
- Errors, shared DTOs, enums -> `types`.

## Migration Steps
1. Inventory current files and tag them using the routing rules.
2. Create the new subpackages and move files accordingly.
3. Extract shared interfaces into `core`, `store`, or `types` as needed.
4. Update imports across the repo to point to new packages.
5. Ensure any public surface remains intact via re-exports if needed.
6. Run tests and adjust any package-level init assumptions.

## Follow-up Tasks
- Add package-level docs for each new subpackage.
- Enforce package boundaries with lint or review checklist.
- Consider a small `internal/ratelimit/README.md` describing layout.
