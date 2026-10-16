# check runs what CI runs. Run it before every push.
.PHONY: check
check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "Not formatted. Run: gofmt -w ."; exit 1; }
	go vet ./...
	@if command -v golangci-lint >/dev/null; then golangci-lint run; \
	else echo "golangci-lint is not installed, so lint first runs in CI. Install it: brew install golangci-lint"; fi
	go test -race ./...
