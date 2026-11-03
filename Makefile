# AirDroid: Go alone, no cgo. Building needs Go 1.27 and network access once,
# for scrcpy's release (scripts/fetch-scrcpy.sh).

GO ?= go
export CGO_ENABLED = 0

.PHONY: deps dev build build-arm64 test vet clean

resources/darwin/scrcpy-server:
	./scripts/fetch-scrcpy.sh

deps: resources/darwin/scrcpy-server

# Runs the app, rebuilt as its sources change.
dev: deps
	$(GO) tool mygo dev

# AirDroid.app and its disk image, for Apple silicon and Intel, in build/.
build: deps
	$(GO) tool mygo build -platform darwin/universal

build-arm64: deps
	$(GO) tool mygo build -platform darwin/arm64

test:
	$(GO) test ./...

vet:
	GOOS=darwin GOARCH=arm64 $(GO) vet ./...
	GOOS=darwin GOARCH=amd64 $(GO) vet ./...
	$(GO) tool mygo vet .

clean:
	rm -rf build .mygo
