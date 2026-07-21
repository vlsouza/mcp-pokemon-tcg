BINARY      := pokemon-tcg-mcp
WINDOWS_BIN := pokemon-tcg-mcp.exe

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*##"}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: ## Run the server on stdio
	go run .

.PHONY: build
build: ## Build the Linux/macOS binary
	go build -o $(BINARY) .

.PHONY: build-windows
build-windows: ## Cross-compile the Windows binary (for Claude Desktop)
	GOOS=windows GOARCH=amd64 go build -o $(WINDOWS_BIN) .

.PHONY: build-all
build-all: build build-windows ## Build both binaries

.PHONY: fmt
fmt: ## Format all Go files in place
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file isn't gofmt-formatted
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-formatted:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: test
test: ## Run tests
	go test ./...

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy

.PHONY: check
check: fmt-check vet test ## Run everything expected before a commit

.PHONY: clean
clean: ## Remove built binaries
	rm -f $(BINARY) $(WINDOWS_BIN)
