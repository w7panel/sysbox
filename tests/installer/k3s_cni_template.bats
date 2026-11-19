#!/usr/bin/env bats

setup() {
  export host_k3s_containerd_conf_dir="$BATS_TEST_TMPDIR/containerd"
  export host_k3s_containerd_conf_template="$host_k3s_containerd_conf_dir/config-v3.toml.tmpl"
  export host_k3s_containerd_conf_template_backup="$host_k3s_containerd_conf_template.orig"
  export sysbox_snapshotter_enabled=false NODE_NAME=test-node
  sed -n '/^function write_default_k3s_containerd_template()/,/^}/p' \
    "$BATS_TEST_DIRNAME/../../sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh" > "$BATS_TEST_TMPDIR/functions.sh"
  sed -n '/^function config_k3s_containerd_for_sysbox()/,/^}/p' \
    "$BATS_TEST_DIRNAME/../../sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh" >> "$BATS_TEST_TMPDIR/functions.sh"
  source "$BATS_TEST_TMPDIR/functions.sh"
  host_flatcar_distro() { return 1; }
  restart_k3s() { :; }
  kubectl() { echo "$runtime_version"; }
}

@test "containerd 2.0 template uses bin_dir" {
  runtime_version=containerd://2.0.4-k3s2
  config_k3s_containerd_for_sysbox
  grep -Fq 'bin_dir = {{ printf "%q" . }}' "$host_k3s_containerd_conf_template"
  ! grep -Fq 'bin_dirs =' "$host_k3s_containerd_conf_template"
}

@test "containerd 2.3 template uses bin_dirs" {
  runtime_version=containerd://2.3.4-k3s1
  config_k3s_containerd_for_sysbox
  grep -Fq 'bin_dirs = [{{ printf "%q" . }}]' "$host_k3s_containerd_conf_template"
  ! grep -Fq 'bin_dir =' "$host_k3s_containerd_conf_template"
}
