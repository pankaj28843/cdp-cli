.PHONY: build clean cron-install cron-remove cron-show cross-build e2e e2e-demo e2e-demo-installed e2e-installed e2e-openai-compat e2e-public-sources-installed e2e-transcription-live e2e-transcription-live-installed e2e-web-research-live-installed fixtures-generate fmt fmt-check install leak-check test verify vet

BINARY := bin/cdp
GUIDE := internal/cli/guide.md
PREFIX ?= $(HOME)/.local
VERSION ?= 0.1.0
BUILD_COMMIT := $(shell git rev-parse HEAD 2>/dev/null || printf unknown)
BUILD_DATE := $(shell git show -s --format=%cI HEAD 2>/dev/null || printf unknown)
BUILD_DIRTY := $(shell if test -n "$$(git status --porcelain --untracked-files=normal 2>/dev/null)"; then printf true; else printf false; fi)
BUILD_LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(BUILD_COMMIT) -X main.date=$(BUILD_DATE) -X main.dirty=$(BUILD_DIRTY) -X main.managedBuild=true

build:
	mkdir -p bin
	go build -ldflags "$(BUILD_LDFLAGS)" -o $(BINARY) ./cmd/cdp

cross-build:
	mkdir -p bin
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(BUILD_LDFLAGS)" -o bin/cdp-darwin-arm64 ./cmd/cdp

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*'))"

leak-check:
	bash scripts/leak-check.sh

e2e: build
	bash scripts/e2e.sh ./$(BINARY)

e2e-demo: build
	bash scripts/e2e_demo.sh ./$(BINARY)

install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 "$(BINARY)" "$(DESTDIR)$(PREFIX)/bin/cdp"
	install -d "$(DESTDIR)$(PREFIX)/share/cdp-cli"
	install -m 0644 "$(GUIDE)" "$(DESTDIR)$(PREFIX)/share/cdp-cli/guide.md"

e2e-installed:
	@cdp_bin="$$(command -v cdp)"; \
	if [ -z "$$cdp_bin" ]; then \
		echo "cdp is not on PATH; run make install or add Go bin to PATH" >&2; \
		exit 2; \
	fi; \
	if [ ! -x "$$cdp_bin" ]; then \
		echo "cdp binary at $$cdp_bin is not executable" >&2; \
		exit 2; \
	fi; \
	bash scripts/e2e.sh "$$cdp_bin"

e2e-openai-compat: build
	bash scripts/e2e_openai_compat.sh ./$(BINARY)

e2e-transcription-live: build
	bash scripts/e2e_transcription_live.sh ./$(BINARY)

e2e-demo-installed:
	@cdp_bin="$$(command -v cdp)"; \
	if [ -z "$$cdp_bin" ]; then \
		echo "cdp is not on PATH; run make install or add Go bin to PATH" >&2; \
		exit 2; \
	fi; \
	if [ ! -x "$$cdp_bin" ]; then \
		echo "cdp binary at $$cdp_bin is not executable" >&2; \
		exit 2; \
	fi; \
	bash scripts/e2e_demo.sh "$$cdp_bin"

e2e-transcription-live-installed:
	@cdp_bin="$$(command -v cdp)"; \
	if [ -z "$$cdp_bin" ]; then \
		echo "cdp is not on PATH; run make install or add Go bin to PATH" >&2; \
		exit 2; \
	fi; \
	if [ ! -x "$$cdp_bin" ]; then \
		echo "cdp binary at $$cdp_bin is not executable" >&2; \
		exit 2; \
	fi; \
	bash scripts/e2e_transcription_live.sh "$$cdp_bin"

e2e-public-sources-installed:
	go run ./cmd/e2e-public-sources --cdp "$$(command -v cdp)"

e2e-web-research-live-installed:
	@cdp_bin="$$(command -v cdp)"; \
	if [ -z "$$cdp_bin" ]; then \
		echo "cdp is not on PATH; run make install or add Go bin to PATH" >&2; \
		exit 2; \
	fi; \
	if [ ! -x "$$cdp_bin" ]; then \
		echo "cdp binary at $$cdp_bin is not executable" >&2; \
		exit 2; \
	fi; \
	bash scripts/e2e_web_research_live.sh "$$cdp_bin"

fixtures-generate:
	python3 scripts/generate_transcription_fixtures.py

cron-install:
	cdp cron install --profile agent --json

cron-show:
	cdp cron status --json

cron-remove:
	cdp cron remove --json

verify: fmt-check test vet cross-build build e2e leak-check

clean:
	rm -rf bin coverage.out
