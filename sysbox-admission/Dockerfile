ARG GO_IMAGE=golang:1.24.3-bookworm
ARG BASE_IMAGE=debian:bookworm-slim

FROM ${GO_IMAGE} AS builder

ARG TARGETARCH=amd64
ARG VERSION=unknown
ARG REVISION=unknown
ARG GOPROXY

WORKDIR /src

COPY sysbox-admission/go.mod sysbox-admission/go.sum ./sysbox-admission/
COPY sysbox-libs/containerdUtils/go.mod ./sysbox-libs/containerdUtils/

WORKDIR /src/sysbox-admission
ENV GOPROXY=${GOPROXY}
RUN go mod download

WORKDIR /src
COPY sysbox-admission ./sysbox-admission
COPY sysbox-libs/containerdUtils ./sysbox-libs/containerdUtils

WORKDIR /src/sysbox-admission
RUN make -B sysbox-admission TARGET_ARCH="${TARGETARCH}" VERSION="${VERSION}" COMMIT="${REVISION}"

FROM ${BASE_IMAGE}

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

ARG TARGETARCH=amd64

COPY --from=builder /src/sysbox-admission/build/${TARGETARCH}/sysbox-admission /usr/local/bin/sysbox-admission

ENTRYPOINT ["/usr/local/bin/sysbox-admission"]
