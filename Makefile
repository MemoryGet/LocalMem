# IClude 构建目标 / IClude build targets
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST := dist

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: build release clean test

# 本地构建 / Local build
build:
	@mkdir -p $(DIST)
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/iclude-mcp ./cmd/mcp/
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/iclude-cli ./cmd/cli/
	@echo "Build complete: $(DIST)/"

# 交叉编译所有平台 / Cross-compile for all platforms
release: clean
	@mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		OS=$${platform%/*}; \
		ARCH=$${platform#*/}; \
		EXT=""; \
		[ "$$OS" = "windows" ] && EXT=".exe"; \
		echo "Building iclude-mcp-$$OS-$$ARCH$$EXT ..."; \
		CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH go build -ldflags "$(LDFLAGS)" \
			-o $(DIST)/iclude-mcp-$$OS-$$ARCH$$EXT ./cmd/mcp/; \
		echo "Building iclude-cli-$$OS-$$ARCH$$EXT ..."; \
		CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH go build -ldflags "$(LDFLAGS)" \
			-o $(DIST)/iclude-cli-$$OS-$$ARCH$$EXT ./cmd/cli/; \
	done
	@echo "Release build complete: $(DIST)/"

# 运行测试 / Run tests
test:
	go test ./testing/... -count=1

clean:
	rm -rf $(DIST)
