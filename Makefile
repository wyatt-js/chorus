BIN := bin/chorus

.PHONY: all deps build test clean

all: build

## deps: build native sidecars (audiotee capture, chorusaudio output helper, airplayrelay AirPlay 2)
deps:
	./scripts/build_deps.sh

## build: build the chorus binary (pure Go: Google Cast + AirPlay 2 + Bluetooth)
build:
	CGO_ENABLED=0 go build -o $(BIN) ./cmd/chorus

## test: run Go unit tests
test:
	go test ./...

## clean: remove build artifacts
clean:
	rm -rf bin
