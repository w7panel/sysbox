#!/usr/bin/env bash
#
# Build a Sysbox release from the local source tree. The default profile builds
# all release artifacts. For a quick nested-runtime regression, patch only the
# changed binaries into a known-good image, for example:
# BUILD_PROFILE=test TEST_COMPONENTS=runc-lite,snapshotter,inner-script \
# TEST_TARGETS=bootstrap TEST_BOOTSTRAP_BASE_IMAGE=repo/bootstrap:base \
# TEST_BOOTSTRAP_IMAGE=repo/bootstrap:test ./w7panel-doc/release.sh
#   1. build the generic sysbox-ce deb
#   2. build and verify the K3s deploy image
#   3. write release artifacts under dist/
#   4. publish a GitHub Release when GITHUB_TOKEN is set

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PKGR_DIR="${ROOT_DIR}/sysbox-pkgr"
K8S_DIR="${PKGR_DIR}/k8s"
CHART_DIR="${CHART_DIR:-${ROOT_DIR}/charts/w7panel-sysbox}"
DIST_DIR="${DIST_DIR:-${ROOT_DIR}/dist}"
CACHE_DIR="${SYSBOX_CACHE_DIR:-${ROOT_DIR}/.cache/sysbox}"
BUILD_PROFILE="${BUILD_PROFILE:-release}"
TEST_COMPONENTS="${TEST_COMPONENTS:-}"
TEST_TARGETS="${TEST_TARGETS:-bootstrap}"
TEST_BASE_IMAGE="${TEST_BASE_IMAGE:-}"
TEST_BOOTSTRAP_BASE_IMAGE="${TEST_BOOTSTRAP_BASE_IMAGE:-}"
TEST_IMAGE="${TEST_IMAGE:-}"
TEST_BOOTSTRAP_IMAGE="${TEST_BOOTSTRAP_IMAGE:-}"
CLEAR_BUILD_CACHE="${CLEAR_BUILD_CACHE:-false}"

detect_sys_arch() {
    case "$(uname -m)" in
        x86_64)
            echo amd64
            ;;
        aarch64|arm64)
            echo arm64
            ;;
        *)
            return 1
            ;;
    esac
}

HOST_SYS_ARCH="$(detect_sys_arch || true)"
SYS_ARCH="${SYS_ARCH:-${HOST_SYS_ARCH}}"
SYSBOX_VERSION="${SYSBOX_VERSION:-$(cat "${ROOT_DIR}/VERSION")}"
SYSBOX_VERSION_FULL="$(echo "${SYSBOX_VERSION}" | sed '/-[0-9]/!s/.*/&-0/')"
RELEASE_TAG="${RELEASE_TAG:-v${SYSBOX_VERSION_FULL}}"
RELEASE_NAME="${RELEASE_NAME:-Sysbox ${RELEASE_TAG}}"
RELEASE_BODY="${RELEASE_BODY:-Sysbox ${RELEASE_TAG} release artifacts.}"

# Keep upstream services as the default. Set MIRROR_PROFILE=china to apply
# domestic defaults; every individual variable below can still be overridden.
MIRROR_PROFILE="${MIRROR_PROFILE:-foreign}"
if [[ "${MIRROR_PROFILE}" == "china" ]]; then
    IMAGE_REPO="${IMAGE_REPO:-docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s}"
    K3S_BASE_IMAGE="${K3S_BASE_IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/centos-centos:stream9}"
    UBUNTU_MIRROR="${UBUNTU_MIRROR:-http://mirrors.aliyun.com/ubuntu}"
    DOCKER_APT_MIRROR="${DOCKER_APT_MIRROR:-https://mirrors.aliyun.com/docker-ce/linux/ubuntu}"
    GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
    GITHUB_PROXY="${GITHUB_PROXY:-https://gh-proxy.org/}"
fi

# China mirror alternative:
# IMAGE_REPO=docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s
IMAGE_REPO="${IMAGE_REPO:-ghcr.io/w7panel/sysbox-deploy-k3s}"
IMAGE_TAG="${IMAGE_TAG:-${RELEASE_TAG}}"
IMAGE="${IMAGE:-${IMAGE_REPO}:${IMAGE_TAG}}"
# The bootstrap image is consumed by CKM's initContainer before its nested K3s
# starts. It is intentionally flattened: some nested containerd versions fail
# while extracting the normal multi-layer deploy image.
BUILD_BOOTSTRAP_IMAGE="${BUILD_BOOTSTRAP_IMAGE:-false}"
BOOTSTRAP_IMAGE_REPO="${BOOTSTRAP_IMAGE_REPO:-${IMAGE_REPO}-bootstrap}"
BOOTSTRAP_IMAGE_TAG="${BOOTSTRAP_IMAGE_TAG:-${IMAGE_TAG}}"
BOOTSTRAP_IMAGE="${BOOTSTRAP_IMAGE:-${BOOTSTRAP_IMAGE_REPO}:${BOOTSTRAP_IMAGE_TAG}}"
PUSH_IMAGE="${PUSH_IMAGE:-false}"
SAVE_IMAGE_TAR="${SAVE_IMAGE_TAR:-false}"
VERIFY_IMAGE="${VERIFY_IMAGE:-true}"
USE_BUILDX="${USE_BUILDX:-false}"
PACKAGE_CHART="${PACKAGE_CHART:-true}"
CHART_VERSION="${CHART_VERSION:-${SYSBOX_VERSION_FULL}}"
K3S_BASE_IMAGE="${K3S_BASE_IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/centos-centos:stream9}"
SYSBOX_RUNC_LITE_BINARY="${SYSBOX_RUNC_LITE_BINARY:-}"

# China mirror alternatives:
# UBUNTU_MIRROR=http://mirrors.aliyun.com/ubuntu
# DOCKER_APT_MIRROR=https://mirrors.aliyun.com/docker-ce/linux/ubuntu
# GOPROXY=https://goproxy.cn,direct
UBUNTU_MIRROR="${UBUNTU_MIRROR:-http://archive.ubuntu.com/ubuntu}"
DOCKER_APT_MIRROR="${DOCKER_APT_MIRROR:-https://download.docker.com/linux/ubuntu}"
GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"
GITHUB_PROXY="${GITHUB_PROXY:-https://gh-proxy.org/}"

DOCKER="${DOCKER:-docker}"
GIT="${GIT:-git}"
CURL="${CURL:-curl}"
JQ="${JQ:-jq}"
DPKG="${DPKG:-dpkg}"
HELM="${HELM:-helm}"
GITHUB_API_URL="${GITHUB_API_URL:-https://api.github.com}"
GITHUB_UPLOAD_URL="${GITHUB_UPLOAD_URL:-https://uploads.github.com}"
GITHUB_REPOSITORY="${GITHUB_REPOSITORY:-}"
TARGET_COMMITISH="${TARGET_COMMITISH:-}"
GITHUB_TOKEN="${GITHUB_TOKEN:-}"

info() { printf '[INFO] %s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*" >&2; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

setup_build_cache() {
    [[ "${CLEAR_BUILD_CACHE}" == true ]] && rm -rf "${CACHE_DIR}"
    mkdir -p "${CACHE_DIR}/go-build" "${CACHE_DIR}/go-mod" "${CACHE_DIR}/go-path"
    export GOCACHE="${GOCACHE:-${CACHE_DIR}/go-build}"
    export GOMODCACHE="${GOMODCACHE:-${CACHE_DIR}/go-mod}"
    export GOPATH="${GOPATH:-${CACHE_DIR}/go-path}"
    if [[ -z "${DOCKER_CONFIG:-}" ]]; then
        export DOCKER_CONFIG="${CACHE_DIR}/docker"
        mkdir -p "${DOCKER_CONFIG}"
        # Buildx writes activity state below DOCKER_CONFIG. Preserve an
        # existing login when available, but never modify its read-only source.
        if [[ -r /root/.docker/config.json && ! -e "${DOCKER_CONFIG}/config.json" ]]; then
            cp /root/.docker/config.json "${DOCKER_CONFIG}/config.json"
        fi
    fi
}

contains_csv() { [[ ",$1," == *",$2,"* ]]; }

need_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

validate_arch() {
    case "${SYS_ARCH}" in
        amd64|arm64)
            ;;
        "")
            die "failed to detect host architecture; set SYS_ARCH=amd64 or SYS_ARCH=arm64"
            ;;
        *)
            die "unsupported SYS_ARCH=${SYS_ARCH}; K3s deploy image release supports amd64 and arm64"
            ;;
    esac

    if [[ -n "${HOST_SYS_ARCH}" && "${SYS_ARCH}" != "${HOST_SYS_ARCH}" && "${ALLOW_CROSS_ARCH:-false}" != "true" ]]; then
        die "cross-arch release is not enabled: host=${HOST_SYS_ARCH}, SYS_ARCH=${SYS_ARCH}; run on a ${SYS_ARCH} host or set ALLOW_CROSS_ARCH=true after configuring Docker buildx/qemu"
    fi

    if [[ -n "${HOST_SYS_ARCH}" && "${SYS_ARCH}" != "${HOST_SYS_ARCH}" ]]; then
        USE_BUILDX="true"
    fi
}

json_escape() {
    "${JQ}" -Rs .
}

detect_github_repository() {
    local remote

    if [[ -n "${GITHUB_REPOSITORY}" ]]; then
        return
    fi

    remote="$("${GIT}" -C "${ROOT_DIR}" config --get remote.origin.url || true)"
    case "${remote}" in
        git@github.com:*)
            GITHUB_REPOSITORY="${remote#git@github.com:}"
            GITHUB_REPOSITORY="${GITHUB_REPOSITORY%.git}"
            ;;
        https://github.com/*)
            GITHUB_REPOSITORY="${remote#https://github.com/}"
            GITHUB_REPOSITORY="${GITHUB_REPOSITORY%.git}"
            ;;
    esac
}

resolve_target_commitish() {
    if [[ -z "${TARGET_COMMITISH}" ]]; then
        TARGET_COMMITISH="$("${GIT}" -C "${ROOT_DIR}" rev-parse HEAD)"
    fi
}

make_local_source_link() {
    info "Point sysbox-pkgr sources/sysbox to local source tree"
    mkdir -p "${PKGR_DIR}/sources"
    ln -sfn ../.. "${PKGR_DIR}/sources/sysbox"
    printf '%s\n' "${SYSBOX_VERSION}" > "${ROOT_DIR}/VERSION"
}

build_deb() {
    info "Build generic ${SYS_ARCH} deb for ${SYSBOX_VERSION}"
    make -C "${PKGR_DIR}/deb" clean EDITION=ce \
        ARCH="${SYS_ARCH}"
    make_local_source_link
    make -C "${PKGR_DIR}/deb" generic EDITION=ce \
        ARCH="${SYS_ARCH}" \
        UBUNTU_MIRROR="${UBUNTU_MIRROR}" \
        DOCKER_APT_MIRROR="${DOCKER_APT_MIRROR}" \
        GOPROXY="${GOPROXY}"
}

find_deb() {
    find "${PKGR_DIR}/deb/build" -type f -name "sysbox-ce_*_${SYS_ARCH}.deb" | sort | tail -1
}

collect_deb_artifacts() {
    local deb="$1"

    info "Collect deb artifacts"
    rm -rf "${DIST_DIR}"
    mkdir -p "${DIST_DIR}"
    cp "${deb}" "${DIST_DIR}/"
    cp "${PKGR_DIR}/deb/build/${SYS_ARCH}/ubuntu-jammy"/sysbox-ce_* "${DIST_DIR}/" 2>/dev/null || true
    cp "${ROOT_DIR}/sysbox-runc-lite/build/${SYS_ARCH}/sysbox-runc-lite" "${DIST_DIR}/sysbox-runc-lite-${RELEASE_TAG}-${SYS_ARCH}"
}

build_sysbox_runc_lite() {
    local output="${ROOT_DIR}/sysbox-runc-lite/build/${SYS_ARCH}/sysbox-runc-lite"

    info "Build static sysbox-runc-lite for ${SYS_ARCH}"
    mkdir -p "${ROOT_DIR}/sysbox-runc-lite/build/${SYS_ARCH}"
    if [[ -n "${SYSBOX_RUNC_LITE_BINARY}" ]]; then
        [[ -x "${SYSBOX_RUNC_LITE_BINARY}" ]] || die "SYSBOX_RUNC_LITE_BINARY is not executable: ${SYSBOX_RUNC_LITE_BINARY}"
        install -m 0755 "${SYSBOX_RUNC_LITE_BINARY}" "${output}"
    else
        [[ "${SYS_ARCH}" == "${HOST_SYS_ARCH}" ]] || die "cross-arch sysbox-runc-lite build requires SYSBOX_RUNC_LITE_BINARY"
        make -C "${ROOT_DIR}" sysbox-runc-lite TARGET_ARCH="${SYS_ARCH}"
    fi
    [[ -x "${output}" ]] || die "sysbox-runc-lite binary not found: ${output}"
}

extract_bins() {
    local deb="$1"
    local tmpdir

    tmpdir="$(mktemp -d)"
    info "Extract Sysbox binaries from ${deb}"
    "${DPKG}" -x "${deb}" "${tmpdir}"
    mkdir -p "${K8S_DIR}/bin/sysbox-ce/generic"
	install -m 0755 \
	    "${tmpdir}/usr/bin/sysbox-runc" \
	    "${tmpdir}/usr/bin/sysbox-fs" \
	    "${tmpdir}/usr/bin/sysbox-mgr" \
	    "${tmpdir}/usr/bin/sysbox-snapshotter" \
	    "${tmpdir}/usr/bin/sysbox-admission" \
	    "${K8S_DIR}/bin/sysbox-ce/generic/"
# sysbox-inner-k3s.sh uses sysbox-runc-lite for K3s' default runc handler,
# while the generic payload also exposes runc-lite. Keep both aliases in sync
# so a release cannot silently leave the configured runtime stale.
install -m 0755 "${ROOT_DIR}/sysbox-runc-lite/build/${SYS_ARCH}/sysbox-runc-lite" \
	    "${K8S_DIR}/bin/sysbox-ce/generic/runc-lite"
install -m 0755 "${ROOT_DIR}/sysbox-runc-lite/build/${SYS_ARCH}/sysbox-runc-lite" \
	    "${K8S_DIR}/bin/sysbox-ce/generic/sysbox-runc-lite"
    rm -rf "${tmpdir}"
}

build_installer_image() {
    info "Build ${SYS_ARCH} K3s deploy image ${IMAGE}"

    if [[ "${USE_BUILDX}" == "true" ]]; then
        "${DOCKER}" buildx build \
            --platform "linux/${SYS_ARCH}" \
            --load \
            -t "${IMAGE}" \
            --build-arg BASE_IMAGE="${K3S_BASE_IMAGE}" \
            --build-arg sys_arch="${SYS_ARCH}" \
            --build-arg sysbox_version="${RELEASE_TAG}" \
            --build-arg GITHUB_PROXY="${GITHUB_PROXY}" \
            -f "${K8S_DIR}/Dockerfile.sysbox-k3s" \
            "${K8S_DIR}"
    else
        "${DOCKER}" build \
            -t "${IMAGE}" \
            --build-arg BASE_IMAGE="${K3S_BASE_IMAGE}" \
            --build-arg sys_arch="${SYS_ARCH}" \
            --build-arg sysbox_version="${RELEASE_TAG}" \
            --build-arg GITHUB_PROXY="${GITHUB_PROXY}" \
            -f "${K8S_DIR}/Dockerfile.sysbox-k3s" \
            "${K8S_DIR}"
    fi
}

build_test_components() {
    [[ -n "${TEST_COMPONENTS}" ]] || die 'BUILD_PROFILE=test requires TEST_COMPONENTS (runc-lite,snapshotter,admission,inner-script)'
    [[ "${SYS_ARCH}" == "${HOST_SYS_ARCH}" ]] || die 'BUILD_PROFILE=test supports only the host architecture'
    contains_csv "${TEST_COMPONENTS}" runc-lite && make -C "${ROOT_DIR}" sysbox-runc-lite TARGET_ARCH="${SYS_ARCH}"
    contains_csv "${TEST_COMPONENTS}" snapshotter && make -C "${ROOT_DIR}" sysbox-snapshotter TARGET_ARCH="${SYS_ARCH}"
    contains_csv "${TEST_COMPONENTS}" admission && make -C "${ROOT_DIR}" sysbox-admission TARGET_ARCH="${SYS_ARCH}"
    for component in ${TEST_COMPONENTS//,/ }; do
        case "${component}" in runc-lite|snapshotter|admission|inner-script) ;; *) die "unknown TEST_COMPONENTS entry: ${component}" ;; esac
    done
}

patch_test_image() {
    local base="$1" output="$2" kind="$3" container
    [[ -n "${base}" && -n "${output}" ]] || die "BUILD_PROFILE=test requires ${kind} base and output images"
    container="$("${DOCKER}" create "${base}" /bin/true)"
    if contains_csv "${TEST_COMPONENTS}" runc-lite; then
        "${DOCKER}" cp "${ROOT_DIR}/sysbox-runc-lite/build/${SYS_ARCH}/sysbox-runc-lite" "${container}:/opt/sysbox/bin/generic/runc-lite"
        "${DOCKER}" cp "${ROOT_DIR}/sysbox-runc-lite/build/${SYS_ARCH}/sysbox-runc-lite" "${container}:/opt/sysbox/bin/generic/sysbox-runc-lite"
    fi
    contains_csv "${TEST_COMPONENTS}" snapshotter && "${DOCKER}" cp "${ROOT_DIR}/sysbox-snapshotter/build/${SYS_ARCH}/sysbox-snapshotter" "${container}:/opt/sysbox/bin/generic/sysbox-snapshotter"
    if contains_csv "${TEST_COMPONENTS}" admission && [[ "${kind}" == deploy ]]; then
        "${DOCKER}" cp "${ROOT_DIR}/sysbox-admission/build/${SYS_ARCH}/sysbox-admission" "${container}:/usr/local/bin/sysbox-admission"
        "${DOCKER}" cp "${ROOT_DIR}/sysbox-admission/build/${SYS_ARCH}/sysbox-admission" "${container}:/opt/sysbox/bin/generic/sysbox-admission"
    fi
    contains_csv "${TEST_COMPONENTS}" inner-script && "${DOCKER}" cp "${K8S_DIR}/scripts/sysbox-inner-k3s.sh" "${container}:/opt/sysbox/scripts/sysbox-inner-k3s.sh"
    "${DOCKER}" commit "${container}" "${output}" >/dev/null
    "${DOCKER}" rm "${container}" >/dev/null
    if [[ "${PUSH_IMAGE}" == true ]]; then
        "${DOCKER}" push "${output}"
    fi
}

build_test_images() {
    build_test_components
    mkdir -p "${DIST_DIR}"
    case "${TEST_TARGETS}" in
        deploy|bootstrap|deploy,bootstrap|bootstrap,deploy) ;;
        *) die 'BUILD_PROFILE=test TEST_TARGETS must be deploy, bootstrap, or deploy,bootstrap' ;;
    esac
    if contains_csv "${TEST_TARGETS}" deploy; then
        patch_test_image "${TEST_BASE_IMAGE}" "${TEST_IMAGE}" deploy
    fi
    if contains_csv "${TEST_TARGETS}" bootstrap; then
        patch_test_image "${TEST_BOOTSTRAP_BASE_IMAGE}" "${TEST_BOOTSTRAP_IMAGE}" bootstrap
    fi
    {
        if contains_csv "${TEST_TARGETS}" deploy; then
            printf 'export SYSBOX_IMAGE_REPO=%q\n' "${TEST_IMAGE%:*}"
            printf 'export SYSBOX_IMAGE_TAG=%q\n' "${TEST_IMAGE##*:}"
        fi
        if contains_csv "${TEST_TARGETS}" bootstrap; then
            printf 'export CKM_INNER_SYSBOX_BOOTSTRAP_IMAGE=%q\n' "${TEST_BOOTSTRAP_IMAGE}"
        fi
        printf 'export SYSBOX_RUNC_LITE_BINARY=%q\n' "${ROOT_DIR}/sysbox-runc-lite/build/${SYS_ARCH}/sysbox-runc-lite"
    } > "${DIST_DIR}/test-images.env"
    info "Fast test artifacts: ${DIST_DIR}/test-images.env"
}

verify_images() {
    [[ "${VERIFY_IMAGE}" == "true" ]] || return 0

    info "Verify image tools and Sysbox versions"
    "${DOCKER}" run --rm "${IMAGE}" kubectl version --client=true
    "${DOCKER}" run --rm "${IMAGE}" crictl --version
	"${DOCKER}" run --rm "${IMAGE}" /opt/sysbox/bin/generic/sysbox-runc --version
	"${DOCKER}" run --rm "${IMAGE}" /opt/sysbox/bin/generic/sysbox-fs --version
	"${DOCKER}" run --rm "${IMAGE}" /opt/sysbox/bin/generic/sysbox-mgr --version
	"${DOCKER}" run --rm "${IMAGE}" /opt/sysbox/bin/generic/sysbox-snapshotter --version
	"${DOCKER}" run --rm "${IMAGE}" /usr/local/bin/sysbox-admission --version
	"${DOCKER}" run --rm "${IMAGE}" /opt/sysbox/bin/generic/runc-lite --version
	"${DOCKER}" run --rm "${IMAGE}" /opt/sysbox/bin/generic/sysbox-runc-lite --version
}

build_bootstrap_image() {
    local container_id

    [[ "${BUILD_BOOTSTRAP_IMAGE}" == "true" ]] || return 0

    info "Flatten bootstrap image ${BOOTSTRAP_IMAGE} from ${IMAGE}"
    container_id="$("${DOCKER}" create "${IMAGE}")"
    # docker import intentionally makes one filesystem layer. The bootstrap is
    # invoked explicitly as /bin/sh by the CKM initContainer, so it needs the
    # payload filesystem rather than the deploy-image CMD or ENTRYPOINT.
    "${DOCKER}" export "${container_id}" | "${DOCKER}" import - "${BOOTSTRAP_IMAGE}"
    "${DOCKER}" rm "${container_id}" >/dev/null

    info "Verify flattened bootstrap payload"
    "${DOCKER}" run --rm "${BOOTSTRAP_IMAGE}" /bin/sh -ec '
        test -x /opt/sysbox/scripts/sysbox-inner-k3s.sh
        test -x /opt/sysbox/bin/generic/sysbox-runc
        test -x /opt/sysbox/bin/generic/runc-lite
	    test -x /opt/sysbox/bin/generic/sysbox-runc-lite
    '
}

push_images() {
    [[ "${PUSH_IMAGE}" == "true" ]] || return 0

    info "Push deploy image ${IMAGE}"
    "${DOCKER}" push "${IMAGE}"

    if [[ "${BUILD_BOOTSTRAP_IMAGE}" == "true" ]]; then
        info "Push bootstrap image ${BOOTSTRAP_IMAGE}"
        "${DOCKER}" push "${BOOTSTRAP_IMAGE}"
    fi
}

write_image_artifacts() {
    info "Write image metadata"
    {
        echo "version=${RELEASE_TAG}"
        echo "sysbox_version=${SYSBOX_VERSION}"
        echo "sysbox_version_full=${SYSBOX_VERSION_FULL}"
        echo "sys_arch=${SYS_ARCH}"
        echo "image=${IMAGE}"
        echo "installer_image=${IMAGE}"
        "${DOCKER}" image inspect "${IMAGE}" --format 'installer_image_id={{.Id}}'
        "${DOCKER}" image inspect "${IMAGE}" --format 'installer_repo_digests={{json .RepoDigests}}'
        if [[ "${BUILD_BOOTSTRAP_IMAGE}" == "true" ]]; then
            echo "bootstrap_image=${BOOTSTRAP_IMAGE}"
            "${DOCKER}" image inspect "${BOOTSTRAP_IMAGE}" --format 'bootstrap_image_id={{.Id}}'
            "${DOCKER}" image inspect "${BOOTSTRAP_IMAGE}" --format 'bootstrap_repo_digests={{json .RepoDigests}}'
        fi
    } > "${DIST_DIR}/image-metadata.txt"

    if [[ "${SAVE_IMAGE_TAR}" == "true" ]]; then
        info "Save image tar"
        "${DOCKER}" save -o "${DIST_DIR}/sysbox-deploy-k3s-${RELEASE_TAG}.tar" "${IMAGE}"
    fi
}

package_chart() {
    [[ "${PACKAGE_CHART}" == "true" ]] || return 0

    [[ -d "${CHART_DIR}" ]] || die "chart directory not found: ${CHART_DIR}"
    need_cmd "${HELM}"

    info "Lint Helm chart ${CHART_DIR}"
    "${HELM}" lint "${CHART_DIR}"

    info "Package Helm chart ${CHART_DIR}"
    "${HELM}" package "${CHART_DIR}" \
        --destination "${DIST_DIR}" \
        --version "${CHART_VERSION}" \
        --app-version "${SYSBOX_VERSION_FULL}"
}

should_publish_asset() {
    local asset_name="$1"

    case "${asset_name}" in
        sysbox-ce_*.linux.tar.gz)
            return 1
            ;;
    esac

    return 0
}

write_checksums() {
    info "Write SHA256SUMS"
    (
        local files=()
        local file

        cd "${DIST_DIR}"
        rm -f SHA256SUMS

        for file in *; do
            [[ -f "${file}" ]] || continue
            should_publish_asset "${file}" || continue
            files+=("${file}")
        done

        sha256sum "${files[@]}" > SHA256SUMS
    )
}

github_api() {
    local method="$1"
    local path="$2"
    local data="${3:-}"

    if [[ -n "${data}" ]]; then
        "${CURL}" -fsS \
            -X "${method}" \
            -H "Accept: application/vnd.github+json" \
            -H "Authorization: Bearer ${GITHUB_TOKEN}" \
            -H "X-GitHub-Api-Version: 2022-11-28" \
            -H "Content-Type: application/json" \
            "${GITHUB_API_URL}${path}" \
            --data "${data}"
    else
        "${CURL}" -fsS \
            -X "${method}" \
            -H "Accept: application/vnd.github+json" \
            -H "Authorization: Bearer ${GITHUB_TOKEN}" \
            -H "X-GitHub-Api-Version: 2022-11-28" \
            "${GITHUB_API_URL}${path}"
    fi
}

get_or_create_release() {
    local body_json
    local name_json
    local release_json
    local tag_json
    local target_json
    local payload

    info "Resolve GitHub release ${GITHUB_REPOSITORY}@${RELEASE_TAG}" >&2
    if release_json="$(github_api GET "/repos/${GITHUB_REPOSITORY}/releases/tags/${RELEASE_TAG}" 2>/dev/null)"; then
        printf '%s\n' "${release_json}"
        return
    fi

    tag_json="$(printf '%s' "${RELEASE_TAG}" | json_escape)"
    name_json="$(printf '%s' "${RELEASE_NAME}" | json_escape)"
    body_json="$(printf '%s' "${RELEASE_BODY}" | json_escape)"
    target_json="$(printf '%s' "${TARGET_COMMITISH}" | json_escape)"
    payload="{\"tag_name\":${tag_json},\"target_commitish\":${target_json},\"name\":${name_json},\"body\":${body_json},\"draft\":false,\"prerelease\":false}"

    info "Create GitHub release ${RELEASE_TAG}" >&2
    github_api POST "/repos/${GITHUB_REPOSITORY}/releases" "${payload}"
}

delete_existing_asset() {
    local release_id="$1"
    local asset_name="$2"
    local asset_id

    asset_id="$(github_api GET "/repos/${GITHUB_REPOSITORY}/releases/${release_id}/assets" \
        | "${JQ}" -r --arg name "${asset_name}" '.[] | select(.name == $name) | .id' \
        | head -1)"

    if [[ -n "${asset_id}" && "${asset_id}" != "null" ]]; then
        info "Delete existing release asset ${asset_name}"
        github_api DELETE "/repos/${GITHUB_REPOSITORY}/releases/assets/${asset_id}" >/dev/null
    fi
}

upload_asset() {
    local release_id="$1"
    local file="$2"
    local asset_name

    asset_name="$(basename "${file}")"
    delete_existing_asset "${release_id}" "${asset_name}"

    info "Upload release asset ${asset_name}"
    "${CURL}" -fsS \
        -X POST \
        -H "Accept: application/vnd.github+json" \
        -H "Authorization: Bearer ${GITHUB_TOKEN}" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        -H "Content-Type: application/octet-stream" \
        "${GITHUB_UPLOAD_URL}/repos/${GITHUB_REPOSITORY}/releases/${release_id}/assets?name=${asset_name}" \
        --data-binary @"${file}" >/dev/null
}

publish_github_release() {
    local release_id
    local release_json

    if [[ -z "${GITHUB_TOKEN}" ]]; then
        warn "Skip GitHub release publish; GITHUB_TOKEN is not set"
        return
    fi

    detect_github_repository
    [[ -n "${GITHUB_REPOSITORY}" ]] || die "GITHUB_REPOSITORY is required when GITHUB_TOKEN is set"
    resolve_target_commitish

    need_cmd "${CURL}"
    need_cmd "${JQ}"

    release_json="$(get_or_create_release)"
    release_id="$(printf '%s\n' "${release_json}" | "${JQ}" -r '.id')"
    [[ -n "${release_id}" && "${release_id}" != "null" ]] || die "failed to resolve GitHub release id"

    for file in "${DIST_DIR}"/*; do
        [[ -f "${file}" ]] || continue
        should_publish_asset "$(basename "${file}")" || continue
        upload_asset "${release_id}" "${file}"
    done

    info "GitHub release published: https://github.com/${GITHUB_REPOSITORY}/releases/tag/${RELEASE_TAG}"
}

main() {
    local deb

    validate_arch
    case "${BUILD_PROFILE}" in release|test) ;; *) die 'BUILD_PROFILE must be release or test' ;; esac
    need_cmd make
    need_cmd "${DOCKER}"
    setup_build_cache

    if [[ "${BUILD_PROFILE}" == test ]]; then
        build_test_images
        return
    fi

    need_cmd "${DPKG}"
    need_cmd "${GIT}"
    need_cmd sha256sum

    build_deb
    build_sysbox_runc_lite

    deb="$(find_deb)"
    [[ -n "${deb}" ]] || die "deb package not found"
    info "Built deb: ${deb}"

    collect_deb_artifacts "${deb}"
    extract_bins "${deb}"
    build_installer_image
    verify_images
    build_bootstrap_image
    push_images
    write_image_artifacts
    package_chart
    write_checksums
    publish_github_release

    info "Release complete: ${RELEASE_TAG}"
    info "Artifacts: ${DIST_DIR}"
    info "Installer image: ${IMAGE}"
    if [[ "${BUILD_BOOTSTRAP_IMAGE}" == "true" ]]; then
        info "Bootstrap image: ${BOOTSTRAP_IMAGE}"
    fi
}

main "$@"
