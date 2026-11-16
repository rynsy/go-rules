# `make check` is the whole gate. go-rules lints itself with the config it ships.
GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

.PHONY: check config lint test tidy-check

check: tidy-check lint test

config:
	go run ./cmd/rynsy-rules config

lint: config
	$(GOLANGCI) run -c .golangci.rynsy.yml ./...
	go run ./cmd/rynsy-rules check

test:
	go test -race -count=1 -cover ./...

tidy-check:
	go mod tidy -diff
