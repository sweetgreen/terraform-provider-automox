# Development entry points. CI runs the same commands so a green local run means
# the same thing as a green pipeline.

GO ?= go

.PHONY: build
build:
	$(GO) build -v ./...

.PHONY: test
test:               ## Offline unit tests. No network, no credentials.
	$(GO) test ./... -count=1

.PHONY: testacc
testacc:            ## Acceptance tests against a LIVE Automox organization.
	@echo "These create real objects in a live Automox organization."
	@echo "Objects are TESTING-prefixed, target an empty group, and cannot execute."
	TF_ACC=1 $(GO) test ./... -count=1 -v -timeout 30m

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: fmt
fmt:
	gofmt -w .

.PHONY: lint
lint:
	golangci-lint run

.PHONY: vulncheck
vulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: docs
docs:
	$(GO) generate ./...

.PHONY: check
check: build vet test   ## What CI runs on a pull request.
