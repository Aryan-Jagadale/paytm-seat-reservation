FROM golang:1.27.1-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /

COPY --from=builder /out/server /server

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/server"]