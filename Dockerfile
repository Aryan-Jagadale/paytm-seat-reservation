# ---------- Build stage ----------
FROM golang:1.27-alpine AS builder

WORKDIR /app

# Download dependencies first.
# This layer is cached unless go.mod/go.sum changes.
COPY go.mod go.sum ./
RUN go mod download

# Copy application source
COPY . .

# Build a static Linux binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -o /app/server \
    ./cmd/server


# ---------- Runtime stage ----------
FROM alpine:3.22

WORKDIR /app

# Add CA certificates for HTTPS connections.
RUN apk add --no-cache ca-certificates

COPY --from=builder /app/server /app/server
COPY migrations /app/migrations

EXPOSE 8080

CMD ["/app/server"]