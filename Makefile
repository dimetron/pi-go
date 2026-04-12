.PHONY: build test test-unit test-integration test-e2e test-all test-coverage test-ollama check-cve lint vet e2e clean sandbox-run sandbox-log

build:
	go build ./cmd/pi
	go build ./cmd/pi-sandbox

install: build
	go install ./cmd/pi/
	go install ./cmd/pi-sandbox

run: install
	pi --model minimax-m2.7:cloud

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

## OSX sandbox — pi-sandbox embeds pi-profile.sb, resolves params, tails denial logs automatically
sandbox-run: install
ifeq ($(shell uname),Darwin)
	pi-sandbox --model minimax-m2.7:cloud
else
	pi --model minimax-m2.7:cloud
endif

sandbox-log:
ifeq ($(shell uname),Darwin)
	/usr/bin/log show --predicate 'eventMessage CONTAINS "sandbox" AND eventMessage CONTAINS "deny"' --last $(or $(LAST),1m) --style compact
else
	@echo "sandbox-log is only available on macOS"
endif
