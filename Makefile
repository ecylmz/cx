.PHONY: test build dist clean

VERSION ?= dev
LDFLAGS := -s -w -X github.com/ecylmz/cx/internal/cx.Version=$(VERSION)

test:
	go test ./...
	go vet ./...

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o cx ./cmd/cx

dist:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/cx-darwin-arm64 ./cmd/cx
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/cx-darwin-amd64 ./cmd/cx
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/cx-linux-amd64 ./cmd/cx
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/cx-linux-arm64 ./cmd/cx
	(cd dist && sha256sum cx-* > SHA256SUMS 2>/dev/null || shasum -a 256 cx-* > SHA256SUMS)

clean:
	rm -f cx dist/cx-*
