PLUGIN=qoder
OUT=dist
GOOS?=$(shell go env GOOS)
GOARCH?=$(shell go env GOARCH)

ifeq ($(GOOS),windows)
EXT=dll
else ifeq ($(GOOS),darwin)
EXT=dylib
else
EXT=so
endif

.PHONY: build test clean

build:
	mkdir -p $(OUT)/$(GOOS)/$(GOARCH)
	CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o $(OUT)/$(GOOS)/$(GOARCH)/$(PLUGIN).$(EXT) .
	rm -f $(OUT)/$(GOOS)/$(GOARCH)/$(PLUGIN).h

test:
	go test ./...

clean:
	rm -rf $(OUT)
