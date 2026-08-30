# lint is deliberately gofmt + go vet and not golangci-lint: these are the same two
# checks the Go job in .github/workflows/ci.yml runs, so a green `make lint` means a
# green CI, and neither needs anything installed beyond the Go toolchain.
.PHONY: build test lint

build:
	@echo "Building lerian-infra..."
	@go build -o bin/lerian-infra ./cmd/lerian-infra
	@echo "Built bin/lerian-infra"

test:
	@echo "Running Go tests..."
	@go test ./... -cover

lint:
	@echo "Checking gofmt..."
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Not gofmt-formatted:"; echo "$$unformatted"; exit 1; \
	fi
	@echo "Running go vet..."
	@go vet ./...
	@echo "Go lint passed!"
