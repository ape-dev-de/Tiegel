.PHONY: check fmt vet test build run catalog

check: fmt vet test

fmt:
	@out=$$(gofmt -l $$(git ls-files '*.go' ; git ls-files -o --exclude-standard '*.go')); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

test:
	go test -race ./...

build:
	go build -o bin/tiegel ./cmd/tiegel
	go build -o bin/tiegel-catalog ./cmd/tiegel-catalog

run:
	go run ./cmd/tiegel

catalog:
	go run ./cmd/tiegel-catalog
