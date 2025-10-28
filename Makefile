dev: ## run api in dev mode
	air
lint:
	golangci-lint run
test:
	go test ./... -race -cover
migrate-up:
	migrate -path migrations -database $$DATABASE_URL up
sqlc:
	sqlc generate
