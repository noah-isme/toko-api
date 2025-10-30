dev: ## run api in dev mode
	air
lint:
	golangci-lint run
test:
	go test ./... -race -cover
migrate-up:
	migrate -path migrations -database $$DATABASE_URL up
sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate

tenant-guard:
	go run ./cmd/tools/tenant_guard
