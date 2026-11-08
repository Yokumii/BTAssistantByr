.PHONY: fmt fmt-check tidy-check test test-race vet build check ci clean

GO ?= go
BINARY := bin/btassistantbyr
GO_FILES := $(shell find cmd internal -name '*.go')

fmt:
	gofmt -w $(GO_FILES)

fmt-check:
	@unformatted="$$(gofmt -l $(GO_FILES))"; \
	if [ -n "$$unformatted" ]; then \
		printf 'Go files require formatting:\n%s\n' "$$unformatted"; \
		exit 1; \
	fi

tidy-check:
	@GOFLAGS= $(GO) mod tidy -diff

test:
	$(GO) test -count=1 ./...

test-race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

build:
	$(GO) build -o $(BINARY) ./cmd/btassistantbyr

check: test vet build

ci: fmt-check tidy-check test-race vet build

clean:
	$(GO) clean
	rm -f $(BINARY)
