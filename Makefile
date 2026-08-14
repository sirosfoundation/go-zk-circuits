# Adapted from go-trust's Makefile (circuit-distribution-service-spec.md
# Appendix C: confirmed real conventions to copy directly). Key deviation:
# CGO_ENABLED=0 throughout — unlike go-trust, this service has no PKCS#11
# dependency, so a fully static pure-Go binary is available (spec §4.6).

VERSION ?= $(shell git -C . describe --tags --always --dirty --match=v* 2> /dev/null || echo "0.1.0-dev")
GO_VERSION := $(shell grep -E '^go [0-9]+\.[0-9]+\.[0-9]+' go.mod | sed 's/go //g' | tr -d ' ')
GO_VERSION_MINOR := $(shell echo $(GO_VERSION) | sed -E 's/^([0-9]+\.[0-9]+).*/\1/')
LDFLAGS := -ldflags "-X main.Version=${VERSION}"
GOBIN ?= $$(go env GOPATH)/bin

.PHONY: all
all: check-go-version fmt vet test build ## Run all checks and build (CI pipeline)

.PHONY: default
default: build

.PHONY: check-go-version
check-go-version: ## Check if the current Go version matches the one required by go.mod
	@go version | grep -q "go$(GO_VERSION)" || (echo "Error: Go version mismatch. Required: $(GO_VERSION), Current: $$(go version | awk '{print $$3}' | sed 's/go//')" && exit 1)
	@echo "Using Go version: $(GO_VERSION)"

.PHONY: help
help: ## help information about make commands
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: check-go-version build ## Run the zkc server
	./zkc

.PHONY: test
test: check-go-version ## run tests with coverage and race detection
	go test -v -race -timeout 10m -count=1 -p 4 -coverprofile=cover.out -covermode=atomic -coverpkg=./... ./... && \
	go tool cover -func=cover.out | tail -n 1 | awk '{ print "Total coverage: " $$3 }'

.PHONY: test-integration
test-integration: check-go-version build ## run integration tests (start a real server, make HTTP requests)
	@echo "Running integration tests for go-zk-circuits server..."
	go test -tags=integration -v -timeout 5m -count=1 ./cmd/zkc/...

.PHONY: test-all
test-all: test test-integration ## run all tests including integration tests

.PHONY: build
build: check-go-version swagger ## build the zkc server and circuitctl binaries
	CGO_ENABLED=0 go build ${LDFLAGS} -trimpath -o zkc ./cmd/zkc
	CGO_ENABLED=0 go build ${LDFLAGS} -trimpath -o circuitctl ./cmd/circuitctl

.PHONY: swagger
swagger: install-swag ## Generate OpenAPI/Swagger documentation
	$(GOBIN)/swag init -g cmd/zkc/main.go --output docs/swagger 2>&1 | grep -v "warning: failed to evaluate" || true
	@echo "Swagger documentation generated at docs/swagger/"
	@echo "View at: http://localhost:8080/swagger/index.html (when server is running)"

.PHONY: install-swag
install-swag: ## Install swag tool for generating Swagger docs (pinned via go.mod's tool directive)
	@which swag > /dev/null || (echo "Installing swag..." && go install github.com/swaggo/swag/cmd/swag)

.PHONY: clean
clean: ## remove temporary files
	go clean
	rm -rf docs/swagger
	rm -f zkc circuitctl *.out *.log

.PHONY: deps
deps: ## Update dependencies
	go get -u ./...
	@echo "Don't forget to run 'make tidy' to clean up the go.mod file"

.PHONY: tidy
tidy: ## Clean up dependencies
	go mod tidy

.PHONY: gosec
gosec: ## Run security checks with gosec
	gosec -exclude=G107 -color -nosec -tests ./...

.PHONY: staticcheck
staticcheck: ## Run static analysis with staticcheck
	staticcheck ./...

.PHONY: lint
lint: ## Run linters (golangci-lint, gosec, staticcheck)
	golangci-lint run ./...
	$(MAKE) gosec
	$(MAKE) staticcheck

.PHONY: fmt
fmt: ## Format all Go code with gofmt
	@gofmt -s -w .
	@echo "Code formatted"

.PHONY: vet
vet: ## Run go vet on all packages
	go vet ./...

.PHONY: coverage
coverage: ## Generate and display coverage report
	go test ./... -coverprofile=cover.out -covermode=atomic
	go tool cover -func=cover.out

.PHONY: tools
tools: ## Install development tools (all pinned via go.mod's tool directive, not @latest)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint
	go install github.com/securego/gosec/v2/cmd/gosec
	go install honnef.co/go/tools/cmd/staticcheck
	go install golang.org/x/vuln/cmd/govulncheck

.PHONY: quick
quick: fmt vet ## Quick checks (fmt + vet) before commit

.PHONY: ci
ci: all ## Run CI pipeline (same as 'all')

.PHONY: docker
docker: check-go-version build ## Build the Docker image
	docker build -t go-zk-circuits:${VERSION} -t go-zk-circuits:latest .

.PHONY: publish-v8
publish-v8: build ## Regenerate the manifest from catalog/circuits/*.json (does NOT add new entries)
	./circuitctl verify
