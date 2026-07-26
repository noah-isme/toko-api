dev: ## run api in dev mode
	go run github.com/air-verse/air@latest
lint:
	golangci-lint run
test:
	go test ./... -race -cover
# Uses ./cmd/migrate, which embeds the SQL files, so local runs apply exactly
# what a deployed image would. No external migrate CLI needed.
migrate-up:
	go run ./cmd/migrate up
migrate-down:
	go run ./cmd/migrate down
migrate-version:
	go run ./cmd/migrate version
sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate

tenant-guard:
	go run ./cmd/tools/tenant_guard
