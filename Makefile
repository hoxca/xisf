GO      ?= go
PKGS    ?= ./...
TIMEOUT ?= 600
V       ?= 0
Q        = $(if $(filter 1,$V),,@)
M        = $(shell printf "\033[34;1m▶\033[0m")

COVERAGE_DIR     = test
COVERAGE_PROFILE = $(COVERAGE_DIR)/coverage.out

# fmt rewrites files, so keep the targets strictly sequential.
.NOTPARALLEL:

.PHONY: all
all: fmt lint build ## Format, lint and compile every package

.PHONY: check
check: all test ## Format, lint, compile and run the full test suite

.PHONY: build
build: ## Compile every package (library, no output binary)
	@echo "$(M) building…"
	$Q $(GO) build $(PKGS)

.PHONY: test
test: ## Run the full test suite
	@echo "$(M) running tests…"
	$Q $(GO) test -timeout $(TIMEOUT)s $(PKGS)

.PHONY: test-short
test-short: ## Run only short tests
	@echo "$(M) running short tests…"
	$Q $(GO) test -short -timeout $(TIMEOUT)s $(PKGS)

.PHONY: test-race
test-race: ## Run tests with the race detector
	@echo "$(M) running race tests…"
	$Q $(GO) test -race -timeout $(TIMEOUT)s $(PKGS)

.PHONY: test-bench
test-bench: ## Run benchmarks
	@echo "$(M) running benchmarks…"
	$Q $(GO) test -run=__absolutelynothing__ -bench=. -timeout $(TIMEOUT)s $(PKGS)

.PHONY: test-verbose
test-verbose: ## Run the full test suite verbosely
	@echo "$(M) running tests…"
	$Q $(GO) test -v -timeout $(TIMEOUT)s $(PKGS)

.PHONY: test-coverage
test-coverage: ## Write a coverage profile to test/coverage.out
	@echo "$(M) running coverage tests…"
	$Q mkdir -p $(COVERAGE_DIR)
	$Q $(GO) test -covermode=atomic -coverprofile=$(COVERAGE_PROFILE) -timeout $(TIMEOUT)s $(PKGS)

.PHONY: fmt
fmt: ## Format all Go source files
	@echo "$(M) running gofmt…"
	$Q $(GO) fmt $(PKGS)

.PHONY: lint
lint: ## Run golangci-lint over every package
	@echo "$(M) running golangci-lint…"
	$Q golangci-lint run $(PKGS)

.PHONY: vet
vet: ## Run go vet
	@echo "$(M) running go vet…"
	$Q $(GO) vet $(PKGS)

.PHONY: clean
clean: ## Remove build and coverage artifacts
	@echo "$(M) cleaning…"
	@rm -rf bin $(COVERAGE_DIR)

.PHONY: version
version: ## Print the current version
	@git describe --tags --always --match=v* 2> /dev/null || \
		cat $(CURDIR)/.version 2> /dev/null || echo v0

.PHONY: help
help:
	@grep -hE '^[ a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-17s\033[0m %s\n", $$1, $$2}'
