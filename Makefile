.PHONY: build run tui test cover fmt vet lint clean

# Build the acc binary into the current directory.
build:
	go build -o acc .

# Run the proxy against the local config.json.
run:
	go run . -config config.json

# Run with the interactive terminal dashboard.
tui:
	go run . -config config.json -tui

# Run the test suite with the race detector.
test:
	go test -race ./...

# Test suite with coverage summary.
cover:
	go test -race -cover ./...

# Format all Go sources.
fmt:
	gofmt -w .

# Static analysis.
vet:
	go vet ./...

# Full pre-commit gate: format check, vet, build, test.
lint: vet
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Needs gofmt:"; echo "$$unformatted"; exit 1; \
	fi
	go build ./...
	go test -race ./...

clean:
	rm -f acc
