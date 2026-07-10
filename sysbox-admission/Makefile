.PHONY: sysbox-admission test gomod-tidy clean distclean

GO := go
GOOS ?= linux
CGO_ENABLED ?= 0
BUILDROOT := build
BUILDDIR = $(BUILDROOT)/$(TARGET_ARCH)
TARGET := sysbox-admission

COMMIT_NO := $(shell git rev-parse HEAD 2> /dev/null || true)
COMMIT ?= $(if $(shell git status --porcelain --untracked-files=no),$(COMMIT_NO)-dirty,$(COMMIT_NO))
VERSION ?= $(shell cat ../VERSION 2>/dev/null || echo unknown)

ifeq ($(TARGET_ARCH),)
	UNAME_M := $(shell uname -m)
	ifeq ($(UNAME_M),x86_64)
		TARGET_ARCH := amd64
	else ifeq ($(UNAME_M),aarch64)
		TARGET_ARCH := arm64
	endif
endif

GOARCH ?= $(TARGET_ARCH)

.DEFAULT: sysbox-admission

sysbox-admission: $(BUILDDIR)/$(TARGET)

$(BUILDDIR)/$(TARGET):
	mkdir -p $(BUILDDIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) $(GO) build -buildvcs=false -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION) -X main.revision=$(COMMIT)" \
		-o $(BUILDDIR)/$(TARGET) ./cmd/sysbox-admission

test:
	$(GO) test -buildvcs=false -timeout 3m -v ./...

gomod-tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BUILDROOT)

distclean: clean
	rm -f go.sum
