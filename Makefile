GO ?= go
COMPOSE = docker compose -f infra/docker/compose.yml
.PHONY: up down seed-demo test test-integration smoke benchmark web demo migrate-up
up:
	$(COMPOSE) up -d --build
down:
	$(COMPOSE) down
migrate-up:
	@echo "Migrations run automatically when the API starts."
seed-demo:
	python3 scripts/seed.py
test:
	$(GO) test -race ./...
	cd apps/web && npm ci && npm run build
test-integration:
	TEST_DATABASE_URL="postgres://beacon:beacon@localhost:5432/beacon?sslmode=disable" $(GO) test -race -count=1 -v ./...
smoke:
	python3 scripts/smoke.py
benchmark:
	python3 scripts/benchmark.py
web:
	cd apps/web && npm ci && npm run dev
demo:
	cd apps/web && npm ci && VITE_DEMO_MODE=true npm run dev
