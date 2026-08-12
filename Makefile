.PHONY: fmt fmt-check test test-race vet build check ci clean

BINARY := bin/btassistantbyr

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

fmt-check:
	@unformatted="$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))"; \
	if [ -n "$$unformatted" ]; then \
		printf 'Go files require formatting:\n%s\n' "$$unformatted"; \
		exit 1; \
	fi

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

build:
	go build -o $(BINARY) ./cmd/btassistantbyr

check: test vet build

ci: fmt-check test-race vet build

clean:
	go clean
	rm -f $(BINARY)
