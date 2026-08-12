.PHONY: fmt test vet build check clean

BINARY := bin/btassistantbyr

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

test:
	go test ./...

vet:
	go vet ./...

build:
	go build -o $(BINARY) ./cmd/btassistantbyr

check: test vet build

clean:
	go clean
	rm -f $(BINARY)
