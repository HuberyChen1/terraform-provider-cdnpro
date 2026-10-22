BINARY  := terraform-provider-cdnpro
VERSION ?= dev

.PHONY: build install test testacc lint fmt docs tidy

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

install:
	go install -ldflags "-X main.version=$(VERSION)" .

test:
	go test ./... -run 'Test[^A]' -count=1

testacc:
	TF_ACC=1 go test ./internal/provider/ -run TestAcc -count=1 -timeout 20m -v

lint:
	test -z "$$(gofmt -l . | tee /dev/stderr)"
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

docs:
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.21.0 generate --provider-name cdnpro
