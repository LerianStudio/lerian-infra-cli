# lint here is gofmt + go vet, and needs nothing installed beyond the Go toolchain.
# CI runs more: .github/workflows/go-pr-analysis.yml calls the shared Go workflow,
# which adds golangci-lint, gosec and a coverage threshold. A green `make lint` is
# the fast local floor, not proof that the pull request will be green.
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
