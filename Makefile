# Convenience targets for local development.
# The OpenWrt package definition lives in the packages feed (net/ztewifi).

GO ?= go
BIN := ztewifi

all: build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w" -o $(BIN) .

install: build
	install -d $(DESTDIR)/usr/bin
	install -m 0755 $(BIN) $(DESTDIR)/usr/bin/$(BIN)

clean:
	rm -f $(BIN)

.PHONY: all build install clean
