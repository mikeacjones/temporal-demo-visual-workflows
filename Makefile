.PHONY: dev web build test worker crash-worker docker

# Everything, locally, with the UI on Vite's dev server.
dev:
	./scripts/dev.sh

# Build the UI into web/build, which the app embeds.
web:
	cd web && npm ci && npm run build

build: web
	go build -o bin/ ./cmd/...

test:
	go test ./...
	cd web && npm run check

# Start a flow worker in the foreground (for example after crash-worker).
worker:
	go run ./cmd/worker

# Kill the flow worker the way a crash would: no graceful shutdown.
crash-worker:
	pkill -9 -f 'bin/worker|go-build.*/worker' || true

docker:
	docker compose up --build
