---
name: sysbox-in-sysbox-analysis
description: Analyze Sysbox-in-Sysbox implementations by comparing a working branch with w7panel, mapping code to nested-runtime behavior, and producing evidence-based tests, limitations, and documentation updates.
---

# Sysbox-in-Sysbox Analysis

Use this skill when a user asks what changed in a Sysbox-in-Sysbox branch, requests a comparison with `w7panel`, needs implementation/test status, or wants findings recorded in project documentation.

## Workflow

1. Identify the repository, current branch, comparison branch, merge base, and clean/dirty worktree. Never infer behavior from commit titles alone. If the comparison ref is absent in a shallow clone, look for a sibling checkout or fetch the named branch; report the fallback source.
2. Analyze the main repository and every submodule separately. Record gitlink changes, submodule commits, and whether referenced commits are available from remotes.
3. Group the diff by behavior: Helm/runtime installation, nested identity and user namespaces, `sysbox-runc`, `sysbox-mgr`, `sysbox-fs`, snapshotter/rootfs, cgroup delegation, CNI/networking, nested K3s agent, and tests/docs.
4. For each group, state changed files, old behavior, new behavior, invariant, and evidence (tests, scripts, or source symbols).
5. Describe topology explicitly as L0 host Kubernetes, L1 Sysbox CKM pod/K3s, and L2 workloads. Treat L3 as historical unless current artifacts prove otherwise.
6. Separate results into implemented, validated, known limitation/explicitly abandoned, and unverified. Do not call a feature solved merely because code exists.
7. Produce a concise report using `references/output-template.md`. When requested, update the repository's Sysbox-in-Sysbox docs, but keep analysis and code changes separate.

## Evidence rules

- Distinguish `standard-subid` from `nested-identity`; verify actual UID/GID maps (`0 0 65536`) and child-userns creation.
- Check nested mode uses NoShift and does not join the L1 user namespace directly.
- Verify cgroup delegation preserves L1 limits while restricting L2 to its delegated subtree.
- Check CNI/netns ownership and rootfs persistence across pod restart, not only pod readiness.
- Treat `/proc` noexec isolation, host resource views, seccomp-notify listener visibility, and multi-tenant isolation as separate limitations.
- Report failed or blocked tests with the exact command and observed error.

## References

- `references/implementation-map.md` — subsystem-to-file mapping.
- `references/branch-comparison.md` — `w7panel` baseline and change inventory.
- `references/test-and-doc-index.md` — validation status and test selection.
- `references/usernamespace-solution.md` — concrete nested user namespace solution.
- `references/output-template.md` — report/documentation template.
