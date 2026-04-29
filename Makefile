.PHONY: build clean e2e e2e-installed fmt fmt-check install leak-check test verify vet

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

install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 "$(BINARY)" "$(DESTDIR)$(PREFIX)/bin/cdp"

e2e-installed:
	@command -v cdp >/dev/null || { echo "cdp is not on PATH; run make install or add Go bin to PATH" >&2; exit 2; }
	bash scripts/e2e.sh "$$(command -v cdp)"

verify: fmt-check test vet build e2e leak-check

clean:
	rm -rf bin coverage.out
