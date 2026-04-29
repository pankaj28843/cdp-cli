.PHONY: build test vet fmt clean

BINARY := bin/cdp

build:
	mkdir -p bin
	go build -o $(BINARY) ./cmd/cdp

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

clean:
	rm -rf bin coverage.out

