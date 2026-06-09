# stash — LAN clipboard & file drop
#
# Lint/security tools are not required to be installed: they run via
# `go run <module>@<version>`, pinned below.

APP_PASSWORD ?= test
DATA_DIR     ?= ./data
BIN_DIR      := bin

STATICCHECK_VERSION := 2025.1.1
GOVULNCHECK_VERSION := v1.1.4
GOSEC_VERSION       := v2.22.4

.DEFAULT_GOAL := help

# ---------------------------------------------------------------- setup

.PHONY: install
install: tools ## Pull all dependencies: Go modules, lint tools, e2e npm deps + Chromium
	go mod download
	cd e2e && npm install && npx playwright install chromium

.PHONY: tools
tools: ## Pre-download the pinned lint/security tools
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) -version
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) -version
	go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -version

# ---------------------------------------------------------------- app

.PHONY: run
run: ## Run the server locally on :7827
	APP_PASSWORD=$(APP_PASSWORD) DATA_DIR=$(DATA_DIR) go run ./src

.PHONY: build
build: ## Build a static binary into bin/stash
	CGO_ENABLED=0 go build -o $(BIN_DIR)/stash ./src

.PHONY: docker-up
docker-up: ## Build and start via docker compose (requires .env with APP_PASSWORD)
	docker compose up --build -d

.PHONY: docker-down
docker-down: ## Stop the docker compose stack
	docker compose down

# ---------------------------------------------------------------- tests

.PHONY: test
test: ## Run unit + integration tests
	go test ./...

.PHONY: test-e2e
test-e2e: e2e/node_modules ## Run Playwright end-to-end browser tests
	cd e2e && npx playwright test

.PHONY: typecheck-e2e
typecheck-e2e: e2e/node_modules ## Typecheck the e2e specs (tsc --noEmit)
	cd e2e && npm run typecheck

e2e/node_modules: e2e/package.json e2e/package-lock.json
	cd e2e && npm install
	touch e2e/node_modules

# ----------------------------------------------------- format / lint

.PHONY: fmt
fmt: ## Format Go sources in place (gofmt)
	gofmt -w src

.PHONY: fmt-check
fmt-check: ## Fail if any Go source is not gofmt-formatted
	@out=$$(gofmt -l src); if [ -n "$$out" ]; then \
		echo "gofmt needed on:"; echo "$$out"; exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run staticcheck
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

# ------------------------------------------------- quality / security

.PHONY: vuln
vuln: ## Scan dependencies for known vulnerabilities (govulncheck)
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: sec
sec: ## Run gosec static security analysis
	go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) ./...

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum are not tidy
	go mod tidy -diff

# ---------------------------------------------------------- bundles

.PHONY: check
check: fmt-check vet lint tidy-check test ## Fast pre-commit gate: format, vet, lint, tidy, tests

.PHONY: audit
audit: vuln sec ## Security checks: govulncheck + gosec

.PHONY: ci
ci: check audit typecheck-e2e ## Everything except the browser e2e suite

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "} {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
