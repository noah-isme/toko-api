# Repository Guidelines

## Project Structure & Module Organization
- `cmd/api`, `cmd/worker`, `cmd/tools/*`: entrypoints for the HTTP API, background worker, and maintenance tools.
- `internal/*`: application modules (handlers, services, middleware, and shared utilities).
- `internal/db/queries`: SQL sources for sqlc; `internal/db/gen`: generated Go code (do not edit by hand).
- `migrations/`: database schema migrations, ordered by prefix.
- `docs/`: API contracts, ops docs, and frontend integration guidance.
- `deploy/`: Kubernetes, Prometheus, and Grafana assets; `perf/`: load/chaos tooling and k6 scripts.
- `openapi.yaml` and `openapi/`: OpenAPI spec and snippets.

## Build, Test, and Development Commands
- `docker-compose up -d`: start local dependencies (Postgres, Redis, etc.).
- `make dev`: run the API with live reload via Air.
- `air`: run the same live-reload loop directly.
- `go build ./cmd/api`: build the API binary locally.
- `make test`: run all Go tests with race detection and coverage.
- `make lint`: run `golangci-lint` on the codebase.
- `make migrate-up`: apply DB migrations using `DATABASE_URL`.
- `make sqlc`: regenerate sqlc output after editing `internal/db/queries`.
- `./verify_all.sh`: smoke-test key HTTP flows against a running API.

## Coding Style & Naming Conventions
- Go formatting: run `gofmt` (tabs for indentation); keep files gofmt-clean.
- Package naming: lowercase, short, and domain-scoped (`internal/cart`, `internal/order`).
- Exported identifiers: `CamelCase`; unexported helpers: `camelCase`.
- Generated code: keep edits in `internal/db/queries` or schema files, then run `make sqlc`.

## Testing Guidelines
- Use Go’s `testing` package (with `testify` where helpful).
- Test files end with `*_test.go`, colocated with the package.
- Prefer table-driven tests for handlers/services; keep mocks in `_test.go`.
- Run `make test` before PRs; use `./verify_all.sh` for end-to-end sanity checks.

## Commit & Pull Request Guidelines
- Commit history follows Conventional Commits (for example, `feat: ...`, `refactor: ...`).
- Keep commits focused and scoped to one change set.
- PRs should include a clear description, linked issue (if any), and testing notes.
- Include migration or OpenAPI updates when you change DB schema or API contracts.
