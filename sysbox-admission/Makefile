.PHONY: sysbox-admission test gomod-tidy clean distclean

GO := go
CGO_ENABLED ?= 0
BUILDROOT := build
BUILDDIR = $(BUILDROOT)/$(TARGET_ARCH)
TARGET := sysbox-admission

ifeq ($(TARGET_ARCH),)
	UNAME_M := $(shell uname -m)
	ifeq ($(UNAME_M),x86_64)
		TARGET_ARCH := amd64
	else ifeq ($(UNAME_M),aarch64)
		TARGET_ARCH := arm64
	endif
endif

.DEFAULT: sysbox-admission

sysbox-admission: $(BUILDDIR)/$(TARGET)

$(BUILDDIR)/$(TARGET):
	mkdir -p $(BUILDDIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build -buildvcs=false -trimpath -ldflags "-s -w" -o $(BUILDDIR)/$(TARGET) ./cmd/sysbox-admission

test:
	$(GO) test -buildvcs=false -timeout 3m -v ./...

gomod-tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BUILDROOT)

distclean: clean
	rm -f go.sum
