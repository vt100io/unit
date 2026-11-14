BIN_DIR      := bin
UNIT_CLI     := $(BIN_DIR)/unit
INSPECTOR    := $(BIN_DIR)/unit-inspector

.PHONY: all build clean vet lint vuln test check

all: build

build:
	@mkdir -p $(BIN_DIR)
	@echo "🔨 Building unit..."
	go build -o $(UNIT_CLI) ./cmd/unit
	@echo "📦 Building unit-inspector (static Linux/amd64)..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(INSPECTOR) ./cmd/unit-inspector
	@echo "✅ Build complete. Binaries are in $(BIN_DIR)/"

vet:
	go vet ./...

# go run builds these with this repo's Go and caches them. Tools installed
# once per machine may be built with an older Go than this repo needs.
lint:
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

test:
	go test ./...

# Run all quality checks
check: vet lint vuln test

clean:
	@echo "🧹 Cleaning up..."
	rm -rf $(BIN_DIR)
