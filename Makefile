.PHONY: build test clean cross

BIN := bin/hashbench

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/hashbench

test:
	CGO_ENABLED=0 go test ./...

clean:
	rm -rf bin

cross:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/hashbench_darwin_amd64 ./cmd/hashbench
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o bin/hashbench_darwin_arm64 ./cmd/hashbench
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/hashbench_linux_amd64 ./cmd/hashbench
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o bin/hashbench_linux_arm64 ./cmd/hashbench
