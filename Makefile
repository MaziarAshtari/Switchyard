VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: help build web binary test check run dev-web up down logs clean

help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-10s %s\n", $$1, $$2}'

build: web binary ## Build the GUI and the binary (bin/switchyard)

web: ## Build the GUI into web/dist
	cd web && pnpm install --frozen-lockfile && pnpm build

binary: ## Build the Go binary with the current web/dist embedded
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/switchyard ./cmd/switchyard

test: ## Run the Go tests
	go test ./...

check: ## Vet, test and lint everything
	go vet ./...
	go test ./...
	cd web && pnpm lint && pnpm build

run: build ## Build and run locally (reads .env if present)
	set -a; [ -f .env ] && . ./.env; set +a; ./bin/switchyard

dev-web: ## Run the Vite dev server (proxies /api and /v1 to :8080)
	cd web && pnpm dev

up: ## Build and start with Docker Compose
	VERSION=$(VERSION) docker compose up -d --build

down: ## Stop the Docker Compose stack
	docker compose down

logs: ## Follow the container logs
	docker compose logs -f

clean: ## Remove build output
	rm -rf bin
	find web/dist -mindepth 1 ! -name .gitkeep -delete 2>/dev/null || true
