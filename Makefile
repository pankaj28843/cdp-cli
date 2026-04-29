.PHONY: build clean e2e e2e-demo e2e-demo-installed e2e-installed fmt fmt-check install leak-check test verify vet

BINARY := bin/cdp
PREFIX ?= $(HOME)/.local

build:
	mkdir -p bin
	go build -o $(BINARY) ./cmd/cdp

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

verify: fmt-check test vet build e2e leak-check

clean:
	rm -rf bin coverage.out
