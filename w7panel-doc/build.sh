#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEBUG_ENV_FILE="${DEBUG_ENV_FILE:-${ROOT_DIR}/dist/debug-image.env}"

BINARIES=(sysbox-runc sysbox-mgr sysbox-fs sysbox-snapshotter sysbox-admission)
IMAGE_CONTAINER=""
SELECTED_MODE=""

info() { printf '[INFO] %s\n' "$*"; }
ok() { printf '[ OK ] %s\n' "$*"; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Usage: build.sh <local|debug|release>

  local    Build the five Sysbox binaries from the current source tree
  debug    Build the binaries, package and push the debug carrier image
  release  Run release.sh with China-accessible defaults
EOF
}

choose_mode() {
    local choice

    [[ -t 0 ]] || { usage >&2; exit 2; }
    PS3="Select build mode: "
    select choice in local debug release quit; do
        case "${choice}" in
            local|debug|release) SELECTED_MODE="${choice}"; return ;;
            quit) exit 0 ;;
            *) printf 'Invalid selection\n' >&2 ;;
        esac
    done
}

cleanup() {
    if [[ -n "${IMAGE_CONTAINER}" ]]; then
        docker rm -f "${IMAGE_CONTAINER}" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

need_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

sha256() {
    sha256sum "$1" | awk '{print $1}'
}

binary_path() {
    case "$1" in
        sysbox-runc) printf '%s\n' "${ROOT_DIR}/sysbox-runc/build/amd64/sysbox-runc" ;;
        sysbox-mgr) printf '%s\n' "${ROOT_DIR}/sysbox-mgr/build/sysbox-mgr" ;;
        sysbox-fs) printf '%s\n' "${ROOT_DIR}/sysbox-fs/build/amd64/sysbox-fs" ;;
        sysbox-snapshotter) printf '%s\n' "${ROOT_DIR}/sysbox-snapshotter/build/amd64/sysbox-snapshotter" ;;
        sysbox-admission) printf '%s\n' "${ROOT_DIR}/sysbox-admission/build/amd64/sysbox-admission" ;;
        *) die "unknown binary: $1" ;;
    esac
}

source_revision_tag() {
    local revision dirty_input dirty_hash

    revision="$(git -C "${ROOT_DIR}" rev-parse --short=12 HEAD)"
    dirty_input="$({
        git -C "${ROOT_DIR}" diff --binary
        git -C "${ROOT_DIR}" diff --cached --binary
        git -C "${ROOT_DIR}" ls-files --others --exclude-standard -z | xargs -0r sha256sum
        git -C "${ROOT_DIR}" submodule status --recursive | grep -E '^[+-]' || true
        git -C "${ROOT_DIR}" submodule foreach --quiet --recursive \
            'git diff --binary; git diff --cached --binary; git ls-files --others --exclude-standard -z | xargs -0r sha256sum' 2>/dev/null
    } || true)"
    if [[ -n "${dirty_input}" ]]; then
        dirty_hash="$(printf '%s' "${dirty_input}" | sha256sum | cut -c1-12)"
        printf '%s-dirty-%s\n' "${revision}" "${dirty_hash}"
    else
        printf '%s\n' "${revision}"
    fi
}

build_local() {
    local binary path

    need_cmd make
    need_cmd sha256sum
    export GOPROXY="${GOPROXY:-https://goproxy.cn}"

    make -C "${ROOT_DIR}/sysbox-ipc"
    make -C "${ROOT_DIR}/sysbox-runc"
    make -C "${ROOT_DIR}/sysbox-snapshotter"
    make -C "${ROOT_DIR}/sysbox-admission"
    make -C "${ROOT_DIR}/sysbox-fs"
    make -C "${ROOT_DIR}/sysbox-mgr"

    for binary in "${BINARIES[@]}"; do
        path="$(binary_path "${binary}")"
        [[ -x "${path}" ]] || die "local build did not produce executable: ${path}"
        printf '%-22s %s\n' "${binary}" "$(sha256 "${path}")"
    done
    ok "local binaries built"
}

verify_debug_image() {
    local image="$1" binary local_sha image_sha image_file

    IMAGE_CONTAINER="$(docker create "${image}")"
    for binary in "${BINARIES[@]}"; do
        image_file="$(mktemp "${TMPDIR:-/tmp}/sysbox-image-${binary}.XXXXXX")"
        docker cp "${IMAGE_CONTAINER}:/sysbox-bin/${binary}" "${image_file}"
        local_sha="$(sha256 "$(binary_path "${binary}")")"
        image_sha="$(sha256 "${image_file}")"
        rm -f -- "${image_file}"
        [[ "${local_sha}" == "${image_sha}" ]] || die "image binary mismatch: ${binary}"
        ok "${binary}: local=image=${local_sha}"
    done
    docker rm -f "${IMAGE_CONTAINER}" >/dev/null
    IMAGE_CONTAINER=""
}

build_debug() {
    local image_repo image_tag image base_image repo_digests

    need_cmd docker
    need_cmd git
    docker info >/dev/null || die "docker daemon is unavailable"

    image_repo="${IMAGE_REPO:-docker.cnb.cool/i0358/zpk/sysbox-debug-deploy}"
    [[ "${image_repo}" == docker.cnb.cool/i0358/zpk/* ]] || \
        die "IMAGE_REPO must be under docker.cnb.cool/i0358/zpk/"
    image_tag="${IMAGE_TAG:-$(source_revision_tag)}"
    image="${image_repo}:${image_tag}"
    base_image="${BASE_IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/busybox:1.36}"

    build_local
    info "build debug carrier image: ${image}"
    docker build \
        --build-arg "BASE_IMAGE=${base_image}" \
        -f "${SCRIPT_DIR}/Dockerfile.sysbox-debug-deploy" \
        -t "${image}" \
        "${ROOT_DIR}"
    verify_debug_image "${image}"
    docker push "${image}"

    repo_digests="$(docker image inspect "${image}" --format '{{join .RepoDigests " "}}')"
    mkdir -p "$(dirname "${DEBUG_ENV_FILE}")"
    {
        printf 'export IMAGE=%q\n' "${image}"
        printf 'export IMAGE_REPO=%q\n' "${image_repo}"
        printf 'export IMAGE_TAG=%q\n' "${image_tag}"
    } > "${DEBUG_ENV_FILE}"
    ok "debug image pushed: ${image}"
    [[ -n "${repo_digests}" ]] && info "digest: ${repo_digests}"
    info "deployment environment: ${DEBUG_ENV_FILE}"
    printf '\nsource %q\n' "${DEBUG_ENV_FILE}"
    printf 'export IMAGE=%q\n' "${image}"
    printf 'export IMAGE_REPO=%q\n' "${image_repo}"
    printf 'export IMAGE_TAG=%q\n' "${image_tag}"
}

build_release() {
    info "run release build with China-accessible defaults"
    env \
        IMAGE_REPO="${IMAGE_REPO:-docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s}" \
        K3S_BASE_IMAGE="${K3S_BASE_IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/nestybox-centos7-systemd}" \
        UBUNTU_MIRROR="${UBUNTU_MIRROR:-http://mirrors.aliyun.com/ubuntu}" \
        DOCKER_APT_MIRROR="${DOCKER_APT_MIRROR:-https://mirrors.aliyun.com/docker-ce/linux/ubuntu}" \
        GOPROXY="${GOPROXY:-https://goproxy.cn}" \
        GITHUB_PROXY="${GITHUB_PROXY:-https://gh-proxy.org/}" \
        "${SCRIPT_DIR}/release.sh"
}

main() {
    local mode

    [[ $# -le 1 ]] || { usage >&2; exit 2; }
    mode="${1:-}"
    if [[ -z "${mode}" ]]; then
        choose_mode
        mode="${SELECTED_MODE}"
    fi
    case "${mode}" in
        local) build_local ;;
        debug) build_debug ;;
        release) build_release ;;
        -h|--help) usage ;;
        *) usage >&2; exit 2 ;;
    esac
}

main "$@"
