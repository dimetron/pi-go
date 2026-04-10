.PHONY: build test test-unit test-integration test-e2e test-all test-coverage test-ollama check-cve lint vet e2e clean

build:
	go build ./cmd/pi

install: build
	go install ./cmd/pi/

test: test-unit

test-unit:
	go test ./...

test-integration:
	go test -tags integration ./...

test-e2e:
	go test -tags e2e ./...

# keep old name as alias
e2e: test-e2e

test-all: test-unit test-integration test-e2e

test-coverage:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

test-ollama: build
	@bash scripts/test-ollama-e2e.sh

check-cve:
	go mod tidy -v
	grype db update || :
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./... | grep -A7 Vulnerability || :
	grype .

lint:
	golangci-lint run ./...

vet:
	go vet ./...

clean:
	rm -f pi coverage.out
