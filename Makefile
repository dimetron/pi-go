.PHONY: build test lint e2e clean

build:
	go build ./cmd/pi

test:
	go test ./...

lint:
	go vet ./...

e2e:
	go test -tags e2e ./...

clean:
	rm -f pi
