.PHONY: test build dist clean

test:
	go test ./...
	go vet ./...

build:
	go build -trimpath -ldflags="-s -w" -o cx .

dist:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/cx-darwin-arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/cx-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/cx-linux-arm64 .

clean:
	rm -f cx dist/cx-*
