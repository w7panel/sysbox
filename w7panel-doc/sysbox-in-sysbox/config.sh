#!/usr/bin/env bash
# Shared configuration for the manual Sysbox-in-Sysbox test flow.
# Edit this file, then run the numbered scripts from this directory.

export KUBECONFIG_218="${KUBECONFIG_218:-/root/.kube/218.config}"
export OUTER_NAMESPACE="${OUTER_NAMESPACE:-k3k-console-164315}"
# CKM_NAME/CKM_NAMESPACE are authoritative. The scripts never pick a random
# CKM when several clusters exist.
export CKM_NAMESPACE="${CKM_NAMESPACE:-k3k-console-164315}"
export CKM_NAME="${CKM_NAME:-ckm-6ur35}"
export CKM_SELECTOR="${CKM_SELECTOR:-}"
export CREATE_CKM="${CREATE_CKM:-false}"
export CKM_SELECTED="${CKM_SELECTED:-false}"
export OUTER_RUNTIME_CLASS="${OUTER_RUNTIME_CLASS:-sysbox-runc}"
export SYSBOX_IMAGE_REPO="${SYSBOX_IMAGE_REPO:-docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s}"
export SYSBOX_IMAGE_TAG="${SYSBOX_IMAGE_TAG:-v0.7.1-47-nested-tty-exec}"
export L1_POD="${L1_POD:-}"
export L1_CONTAINER="${L1_CONTAINER:-}"
export CHART_NAMESPACE="${CHART_NAMESPACE:-default}"
export CKM_TEST_DEPLOYMENT="${CKM_TEST_DEPLOYMENT:-ckm-k3s-nginx}"
export CKM_TEST_ROOTFS_PVC="${CKM_TEST_ROOTFS_PVC:-ckm-k3s-nginx-rootfs}"
export CKM_DOCKER_TEST_DEPLOYMENT="${CKM_DOCKER_TEST_DEPLOYMENT:-nested-docker-rootfs-persistence}"
export CKM_DOCKER_TEST_ROOTFS_PVC="${CKM_DOCKER_TEST_ROOTFS_PVC:-nested-docker-rootfs-persistence}"
export CKM_DOCKER_TEST_IMAGE="${CKM_DOCKER_TEST_IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker}"
export CKM_DOCKER_MEMORY_MAX="${CKM_DOCKER_MEMORY_MAX:-2147483648}"
export CKM_DOCKER_CPU_MAX="${CKM_DOCKER_CPU_MAX:-100000 100000}"
export ROOTFS_STORAGE_CLASS="${ROOTFS_STORAGE_CLASS:-local-path}"
export TEST_IMAGE="${TEST_IMAGE:-ccr.ccs.tencentyun.com/afan-public/nginx:latest}"
