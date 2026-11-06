GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

VERSION ?= $(shell git describe --tags --match 'v*' --exclude 'sdk/*' --always --dirty 2>/dev/null | sed 's|^v||')
ifeq ($(VERSION),)
VERSION := dev
endif
LDFLAGS := -X go.ed.linuxfoundation.org/lfed/internal/build.Version=$(VERSION)

.PHONY: setup build test lint tidy-check install-local release-snapshot clean catalog catalog-verify

setup:
	go mod download

GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags '-s -w $(LDFLAGS)'

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOSEC := github.com/securego/gosec/v2/cmd/gosec@v2.30.0
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

build:
	$(GOBUILD) -o bin/lfed ./cmd/lfed

MODULES := . sdk

test:
	for m in $(MODULES); do (cd $$m && go test -race ./...) || exit 1; done

lint:
	test -z "$$(gofmt -l cmd internal sdk)" || { gofmt -l cmd internal sdk; exit 1; }
	for m in $(MODULES); do (cd $$m && go vet ./...) || exit 1; done
	for m in $(MODULES); do (cd $$m && go run $(STATICCHECK) ./...) || exit 1; done
	for m in $(MODULES); do (cd $$m && go run $(GOSEC) -quiet -exclude-generated -exclude=G104,G204,G304,G702 ./...) || exit 1; done
	for m in $(MODULES); do (cd $$m && go run $(GOVULNCHECK) ./...) || exit 1; done

tidy-check:
	for m in $(MODULES); do (cd $$m && go mod tidy -diff) || exit 1; done

install-local:
	$(GOBUILD) -o $(GOBIN)/lfed ./cmd/lfed

clean:
	rm -rf bin dist

release-snapshot:
	goreleaser release --snapshot --clean --skip=publish,sign

# --- plugin catalog --------------------------------------------------------

CATALOG := catalog/catalog.json
CATALOG_PLUGINS := $(sort $(patsubst catalog/plugins/%.yaml,%,$(wildcard catalog/plugins/*.yaml)))
LFED ?= bin/lfed

catalog:
	catalog/build-catalog.sh $(CATALOG)

# Installs every catalog entry with $(LFED). Needs network access to
# ghcr.io and the Sigstore TUF root.
catalog-verify: catalog
	@tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	export LFED_CONFIG_DIR="$$tmp/cfg" LFED_PLUGIN_DIR="$$tmp/plugins"; \
	for name in $(CATALOG_PLUGINS); do \
		echo "==> $(LFED) plugin install $$name"; \
		"$(LFED)" plugin install "$$name" --catalog "$(CATALOG)" || exit 1; \
	done; \
	"$(LFED)" plugin list -o json
