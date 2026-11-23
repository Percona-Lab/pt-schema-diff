# pt-schema-diff
VERSION ?= 0.1.0
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.Build=$(BUILD)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test test-live golden cross clean

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

clean:
	rm -rf bin
