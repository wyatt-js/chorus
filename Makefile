BIN := bin/airtooth

.PHONY: all deps build test clean deps-airplay build-airplay

all: build

## deps: build native sidecars (audiotee capture + airtoothaudio output helper)
deps:
	./scripts/build_deps.sh

## build: build the airtooth binary (pure Go: Google Cast + Bluetooth)
build:
	CGO_ENABLED=0 go build -o $(BIN) ./cmd/airtooth

## test: run Go unit tests
test:
	go test ./...

## clean: remove build artifacts
clean:
	rm -rf bin

## deps-airplay: build the optional classic-AirPlay/RAOP dependency (libraop)
deps-airplay:
	./scripts/build_deps_airplay.sh

## build-airplay: build with the classic-AirPlay/RAOP path enabled (needs deps-airplay)
build-airplay:
	CGO_ENABLED=1 go build -tags airplay -o $(BIN) ./cmd/airtooth
