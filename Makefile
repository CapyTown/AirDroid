# AirDroid: Go alone, no cgo. Building needs Go 1.27 and Python 3, and
# network access once, for adb, scrcpy and FFmpeg (scripts/bundle-*.py).

GO ?= go
PYTHON ?= python3
export CGO_ENABLED = 0

UNAME := $(shell uname -s)
ifeq ($(UNAME),Darwin)
PLATFORM ?= darwin
TARGET ?= darwin/universal
else ifeq ($(OS),Windows_NT)
PLATFORM ?= windows
TARGET ?= windows/amd64
else
PLATFORM ?= linux
TARGET ?= linux/amd64
endif

.PHONY: deps dev build test vet clean

# adb, scrcpy and, on Windows and Linux, FFmpeg, into resources/$(PLATFORM).
deps:
	$(PYTHON) scripts/bundle-adb.py $(PLATFORM)
	$(PYTHON) scripts/bundle-scrcpy.py $(PLATFORM)
	$(PYTHON) scripts/bundle-ffmpeg.py $(PLATFORM)

# Runs the app, rebuilt as its sources change.
dev:
	$(GO) tool mygo dev

# The app and its installer for $(TARGET), in build/.
build:
	$(GO) tool mygo build -platform $(TARGET)

test:
	$(GO) test ./...

vet:
	GOOS=darwin $(GO) vet ./...
	GOOS=windows $(GO) vet ./...
	GOOS=linux $(GO) vet ./...
	$(GO) tool mygo vet .

clean:
	rm -rf build .mygo
