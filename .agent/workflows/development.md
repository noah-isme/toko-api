---
description: Workflow for developing, building, and maintaining the Toko API
---

# Pre-requisites

Ensure you have the following installed:
- Go 1.22+
- Docker & Docker Compose
- Air (for live reload): `go install github.com/air-verse/air@latest`
- golang-migrate (for DB migrations): `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`

# Setup & Run

1. **Environment Setup**
   Ensure `.env` exists and is populated:
   ```bash
   test -f .env || cp .env.example .env
   ```
   
2. **Start Infrastructure**
   Start Postgres and Redis:
   // turbo
   ```bash
   docker-compose up -d
   ```

3. **Run Migrations**
   Load environment variables and run migrations:
   (Note: `make migrate-up` requires DATABASE_URL to be set in the shell)
   ```bash
   export $(grep -v '^#' .env | xargs) && make migrate-up
   ```

4. **Start Development Server**
   Start the API with live reload:
   // turbo
   ```bash
   make dev
   ```

# Database Schema Changes

1. **Create Migration**
   Create a new pair of up/down migration files:
   ```bash
   migrate create -ext sql -dir migrations -seq [your_migration_name]
   ```

2. **Edit Migration Files**
   Add your SQL changes to the generated `.up.sql` and `.down.sql` files in `migrations/`.

3. **Apply Migration**
   ```bash
   export $(grep -v '^#' .env | xargs) && make migrate-up
   ```

4. **Update Queries (Optional)**
   If your schema change requires new or updated queries, edit `.sql` files in `internal/db/queries/`.

5. **Generate Go Code**
   Regenerate the SQLC code:
   // turbo
   ```bash
   make sqlc
   ```

# Quality Assurance

- **Linting**:
  // turbo
  ```bash
  make lint
  ```

- **Testing**:
  // turbo
  ```bash
  make test
  ```
