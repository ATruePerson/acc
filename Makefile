.PHONY: build build-obsidian-plugin run tui test test-obsidian-plugin cover fmt vet lint clean

# Build the acc binary into the current directory.
build:
	go build -o acc .

# Build the standalone Obsidian plugin server. It is not part of ACC core.
build-obsidian-plugin:
	cd plugins/obsidian/server && go build -o ../bin/obsidian-mcp .

# Run the proxy against the local config.json.
run:
	go run . -config config.json

# Run with the interactive terminal dashboard.
tui:
	go run . -config config.json -tui

# Run the test suite with the race detector.
test:
	go test -race ./...

test-obsidian-plugin:
	cd plugins/obsidian/server && go test -race ./...

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
