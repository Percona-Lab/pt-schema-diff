# pt-schema-diff
VERSION ?= 0.1.0
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.Build=$(BUILD)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test test-live golden cross dist clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/pt-schema-diff ./cmd/pt-schema-diff

test:
	go vet ./...
	go test ./...

# Needs a MySQL server, e.g. make test-live DSN=S=/tmp/mysql.sock,u=root
test-live:
	PT_SCHEMA_DIFF_TEST_DSN='$(DSN)' go test -count=1 ./...

# Rewrite testdata/golden after an intended change in the output.
golden:
	go test ./schemadiff -run TestGolden -update

cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building bin/pt-schema-diff-$$os-$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o bin/pt-schema-diff-$$os-$$arch ./cmd/pt-schema-diff || exit 1; \
	done

# Release files in dist/: the four binaries, a Linux bundle (both Linux binaries and a launcher that picks the
# right one) and SHA256SUMS. The release workflow runs this on a tag; it also works locally.
dist: cross
	rm -rf dist && mkdir -p dist/bundle/pt-schema-diff-$(VERSION)-linux
	cp bin/pt-schema-diff-linux-amd64 bin/pt-schema-diff-linux-arm64 bin/pt-schema-diff-darwin-amd64 \
		bin/pt-schema-diff-darwin-arm64 dist/
	cp bin/pt-schema-diff-linux-amd64 bin/pt-schema-diff-linux-arm64 README.md LICENSE dist/bundle/pt-schema-diff-$(VERSION)-linux/
	cp packaging/pt-schema-diff-launcher.sh dist/bundle/pt-schema-diff-$(VERSION)-linux/pt-schema-diff
	chmod 755 dist/bundle/pt-schema-diff-$(VERSION)-linux/pt-schema-diff*
	tar -C dist/bundle -czf dist/pt-schema-diff-$(VERSION)-linux.tar.gz --owner=0 --group=0 pt-schema-diff-$(VERSION)-linux 2>/dev/null \
		|| tar -C dist/bundle -czf dist/pt-schema-diff-$(VERSION)-linux.tar.gz pt-schema-diff-$(VERSION)-linux
	rm -rf dist/bundle
	cd dist && (sha256sum pt-schema-diff-* 2>/dev/null || shasum -a 256 pt-schema-diff-*) > SHA256SUMS
	@cat dist/SHA256SUMS

clean:
	rm -rf bin dist
