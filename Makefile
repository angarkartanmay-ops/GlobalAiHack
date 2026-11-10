# Heimdall: common tasks.

VERSION ?= 1.0.0

.PHONY: build ui test e2e run docker fmt vet

build: ui ## Build the dashboard and the heimdall binary into ./bin
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/heimdall ./cmd/heimdall

ui: ## Build the dashboard into web/dist (embedded in the binary)
	cd web && npm ci --no-audit --no-fund && npm run build

test: ## Unit and integration tests; needs only Go
	go vet ./...
	go test -count=1 ./...

e2e: ## End-to-end against a real Bifrost binary: make e2e BIFROST_BIN=/path/to/bifrost-http
	go test -tags e2e -count=1 -v ./e2e

run: ## Run Heimdall and a Bifrost gateway locally in simulator mode
	scripts/dev-sim.sh

docker: ## Build the all-in-one image
	docker build -t heimdall:$(VERSION) --build-arg VERSION=$(VERSION) .

fmt:
	gofmt -w cmd internal e2e web/embed.go

vet:
	go vet -tags e2e ./...
