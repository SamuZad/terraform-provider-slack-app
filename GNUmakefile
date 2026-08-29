default: test

# Writes the provider binary to the repo root, where the ~/.terraformrc
# dev_overrides entry can pick it up.
.PHONY: build
build:
	go build -v -o terraform-provider-slack-app .

.PHONY: fmt
fmt:
	gofmt -w .

.PHONY: vet
vet:
	go vet ./...

# Unit and acceptance tests. The acceptance tests run against an in-process
# fake Slack API, so no credentials or TF_ACC are required — only a local
# terraform binary.
.PHONY: test
test:
	go test ./... -v $(TESTARGS) -timeout 10m

# Regenerate registry documentation in docs/.
.PHONY: generate
generate:
	go generate ./...
