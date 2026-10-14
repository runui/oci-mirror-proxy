# Build and quality targets for oci-mirror-proxy.

BINARY    := oci-mirror-proxy
MODULE    := github.com/runui/oci-mirror-proxy
VERSION   ?= dev
LDFLAGS   := -s -w -X $(MODULE)/internal/cli.version=$(VERSION)
DIST      := dist
STATICCHECK ?= staticcheck

.PHONY: all build test race vet fmt-check staticcheck deps-check fuzz check clean

all: check build

build:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY) ./cmd/$(BINARY)

test:
	go test -count=1 ./...

race:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed for:"; echo "$$unformatted"; exit 1; fi

staticcheck:
	$(STATICCHECK) ./...

# The project must not depend on the moby ecosystem. Fail when it does.
deps-check:
	@if go list -deps -test ./... | grep -E '^github\.com/(moby|docker/docker|containerd)/'; then \
		echo "forbidden dependency on the moby ecosystem"; exit 1; \
	else \
		echo "dependency policy OK"; \
	fi

# Short fuzzing run of the path parser. Increase FUZZTIME for deeper runs.
FUZZTIME ?= 30s
fuzz:
	go test -run '^$$' -fuzz FuzzParse -fuzztime $(FUZZTIME) ./internal/ociapi/

check: fmt-check vet test race deps-check

clean:
	rm -rf $(DIST)
