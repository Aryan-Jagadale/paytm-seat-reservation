.PHONY: run build test docker-up docker-down lint

run:
	go run ./cmd/server

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -o bin/server ./cmd/server

test:
	go test -race ./...

docker-up:
	docker compose up --build

docker-down:
	docker compose down

lint:
	go vet ./...