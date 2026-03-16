.PHONY: build test test-unit test-integration test-e2e test-all test-coverage lint e2e clean

build:
	go build ./cmd/pi

test: test-unit

test-unit:
	go test ./...

test-integration:
	go test -tags integration ./...

test-e2e:
	go test -tags e2e ./...

# keep old name as alias
e2e: test-e2e

test-all: test-unit test-e2e

test-coverage:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

lint:
	go vet ./...

clean:
	rm -f pi coverage.out
