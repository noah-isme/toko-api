# Build stage
FROM golang:1.24.3-alpine AS builder

WORKDIR /src

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build every entrypoint the deployment needs. The migrate binary embeds the
# SQL files (see embed.go), so the runtime image ships no migrations directory
# and cannot drift from the schema this commit expects.
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /out/api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /out/worker ./cmd/worker \
    && CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /out/migrate ./cmd/migrate

# Runtime stage
FROM alpine:3.21

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Run unprivileged rather than as root.
RUN adduser -D -u 10001 toko

WORKDIR /app

# Binaries live at /app/<name>, which is what the deploy/k8s manifests invoke.
COPY --from=builder /out/api /app/api
COPY --from=builder /out/worker /app/worker
COPY --from=builder /out/migrate /app/migrate

USER toko

EXPOSE 8080

# Command to run the application
CMD ["/app/api"]
