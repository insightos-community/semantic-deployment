.PHONY: build test lint verify clean

build:
	mkdir -p bin
	go build -trimpath -o bin/semantic-robot-bundle ./cmd/semantic-robot-bundle
	go build -trimpath -o bin/semantic-robot-instance ./cmd/semantic-robot-instance

test:
	go test -race ./...

lint:
	go vet ./...

verify: lint test build

clean:
	rm -rf bin
