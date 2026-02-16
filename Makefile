BINARY := agent-memory
PKG := github.com/rcliao/agent-memory
MAIN := ./cmd/agent-memory

.PHONY: build test vet clean install

build:
	go build -o $(BINARY) $(MAIN)

test:
	go test ./... -v

vet:
	go vet ./...

clean:
	rm -f $(BINARY)

install:
	go install $(MAIN)
