BINARY := bin/api
PKG    := ./...

.PHONY: bootstrap run build test fmt vet tidy db-up db-down

bootstrap:
	./scripts/bootstrap.sh

run:
	set -a; . ./.env; set +a; go run ./cmd/api

build:
	go build -o $(BINARY) ./cmd/api

test:
	go test $(PKG)

fmt:
	go fmt $(PKG)

vet:
	go vet $(PKG)

tidy:
	go mod tidy

db-up:
	docker compose up -d postgres

db-down:
	docker compose down
