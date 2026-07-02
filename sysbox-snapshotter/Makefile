#
# sysbox-snapshotter Makefile
#

.PHONY: sysbox-snapshotter test gomod-tidy clean distclean

GO := go

SYSSNAPSHOTTER_BUILDROOT := build
SYSSNAPSHOTTER_BUILDDIR = $(SYSSNAPSHOTTER_BUILDROOT)/$(TARGET_ARCH)
SYSSNAPSHOTTER_TARGET := sysbox-snapshotter

COMMIT_NO := $(shell git rev-parse HEAD 2> /dev/null || true)
COMMIT ?= $(if $(shell git status --porcelain --untracked-files=no),$(COMMIT_NO)-dirty,$(COMMIT_NO))
BUILT_AT := $(shell date)
BUILT_BY := $(shell git config user.name)

LDFLAGS := -X 'main.edition=${EDITION}' -X main.version=${VERSION} \
		-X main.commitId=$(COMMIT) -X 'main.builtAt=$(BUILT_AT)' \
		-X 'main.builtBy=$(BUILT_BY)'

ifeq ($(TARGET_ARCH),)
	UNAME_M := $(shell uname -m)
	ifeq ($(UNAME_M),x86_64)
		TARGET_ARCH := amd64
	else ifeq ($(UNAME_M),aarch64)
		TARGET_ARCH := arm64
	endif
endif

.DEFAULT: sysbox-snapshotter

sysbox-snapshotter: $(SYSSNAPSHOTTER_BUILDDIR)/$(SYSSNAPSHOTTER_TARGET)

$(SYSSNAPSHOTTER_BUILDDIR)/$(SYSSNAPSHOTTER_TARGET):
	mkdir -p $(SYSSNAPSHOTTER_BUILDDIR)
	$(GO) build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o $(SYSSNAPSHOTTER_BUILDDIR)/$(SYSSNAPSHOTTER_TARGET) ./cmd/sysbox-snapshotter

test:
	$(GO) test -buildvcs=false -timeout 3m -v ./...

gomod-tidy:
	$(GO) mod tidy

clean:
	rm -rf $(SYSSNAPSHOTTER_BUILDROOT)

distclean: clean
	rm -f go.sum
