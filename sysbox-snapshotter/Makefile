#
# sysbox-snapshotter Makefile
#

.PHONY: clean distclean gomod-tidy lint listpackages sysbox-snapshotter test

GO := go

SYSSNAPSHOTTER_BUILDROOT := build
SYSSNAPSHOTTER_BUILDDIR = $(SYSSNAPSHOTTER_BUILDROOT)/$(TARGET_ARCH)
SYSSNAPSHOTTER_TARGET := sysbox-snapshotter
SYSSNAPSHOTTER_DIR := $(CURDIR)
SYSSNAPSHOTTER_SRC := $(shell find . 2>&1 | grep -E '.*\.(c|h|go)$$')

ifeq ($(TARGET_ARCH),)
	UNAME_M := $(shell uname -m)
	ifeq ($(UNAME_M),x86_64)
		TARGET_ARCH := amd64
	else ifeq ($(UNAME_M),aarch64)
		TARGET_ARCH := arm64
	else ifeq ($(UNAME_M),arm)
		TARGET_ARCH := armhf
	else ifeq ($(UNAME_M),armel)
		TARGET_ARCH := armel
	endif
endif

PKG_MAIN := github.com/w7panel/sysbox/sysbox-snapshotter/cmd/sysbox-snapshotter
PKG_VERSION := $(PKG_MAIN)/version

COMMIT_NO := $(shell git rev-parse HEAD 2> /dev/null || true)
COMMIT ?= $(if $(shell git status --porcelain --untracked-files=no),$(COMMIT_NO)-dirty,$(COMMIT_NO))
VERSION ?= $(shell cat ../VERSION 2>/dev/null || git describe --match 'v[0-9]*' --dirty='.m' --always --tags)

LDFLAGS := -s -w -X $(PKG_VERSION).Version=$(VERSION) -X $(PKG_VERSION).Revision=$(COMMIT)

# Set cross-compilation flags if applicable.
ifneq ($(SYS_ARCH),$(TARGET_ARCH))
	ifeq ($(TARGET_ARCH),armel)
		GO_XCOMPILE := CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6
	else ifeq ($(TARGET_ARCH),armhf)
		GO_XCOMPILE := CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7
	else ifeq ($(TARGET_ARCH),arm64)
		GO_XCOMPILE := CGO_ENABLED=0 GOOS=linux GOARCH=arm64
	else ifeq ($(TARGET_ARCH),amd64)
		GO_XCOMPILE := CGO_ENABLED=0 GOOS=linux GOARCH=amd64
	endif
endif

.DEFAULT: sysbox-snapshotter

sysbox-snapshotter: $(SYSSNAPSHOTTER_BUILDDIR)/$(SYSSNAPSHOTTER_TARGET)

$(SYSSNAPSHOTTER_BUILDDIR)/$(SYSSNAPSHOTTER_TARGET): $(SYSSNAPSHOTTER_SRC)
	mkdir -p $(SYSSNAPSHOTTER_BUILDDIR)
	$(GO_XCOMPILE) $(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" \
		-o $(SYSSNAPSHOTTER_BUILDDIR)/$(SYSSNAPSHOTTER_TARGET) $(PKG_MAIN)

test:
	$(GO) test -buildvcs=false -timeout 3m -v ./...

gomod-tidy:
	$(GO) mod tidy

lint:
	$(GO) vet $(allpackages)
	$(GO) fmt $(allpackages)

listpackages:
	@echo $(allpackages)

clean:
	rm -f $(SYSSNAPSHOTTER_BUILDDIR)/$(SYSSNAPSHOTTER_TARGET)

distclean: clean
	rm -rf $(SYSSNAPSHOTTER_BUILDROOT)

# memoize allpackages, so that it's executed only once and only if used
_allpackages = $(shell $(GO) list ./... | grep -v vendor)
allpackages = $(if $(__allpackages),,$(eval __allpackages := $$(_allpackages)))$(__allpackages)
