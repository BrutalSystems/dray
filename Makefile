.PHONY: build test lint snapshot clean

build:
	go build -o bin/ ./cmd/...

test:
	go vet ./...
	go test -race ./...

lint:
	golangci-lint run

snapshot:
	goreleaser build --snapshot --clean

clean:
	rm -rf bin dist
