# Branch comparison guide

Compare `w7panel-sysboxin` with `w7panel` at `70f2416c1026bff3ff7cf8de6e536284a9dc2e52`; the observed nested tip is `3af3765e909ec556b4ed3edbb377f180b55acafb`. Recompute statistics with `git diff --stat`.

Use `git status`, `git merge-base`, `git diff --name-status`, `git log`, and `git submodule status`. If a shallow clone lacks `w7panel`, use a sibling checkout or fetch it and report that fallback. Inspect each submodule's gitlink and commit reachability independently. Keep unrelated server/UI changes out of this Sysbox-only report.

For every subsystem state: baseline behavior, concrete files changed, new invariant, and test/source evidence.
