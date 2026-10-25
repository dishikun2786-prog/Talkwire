#!/usr/bin/env bash
# Static checks for the Go modules: gofmt, go vet and, when installed,
# staticcheck.
#
#   tools/lint.sh            # every Go module in the repository
#
# staticcheck is not vendored or installed by this script: install it once
# (go install honnef.co/go/tools/cmd/staticcheck@<version that supports the
# toolchain>) and keep it on PATH. Without it the script says so and still
# runs the other checks, so a missing tool is visible rather than silently
# skipped.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
modules=(server tools/conformance tools/loadtest)
fail=0

for m in "${modules[@]}"; do
	echo "== $m"
	cd "$root/$m" || exit 2
	# The reference server's frozen source is not ours to format.
	unformatted=$(gofmt -l . | grep -v '^oracle[/\\]' || true)
	if [ -n "$unformatted" ]; then
		echo "gofmt: $unformatted"
		fail=1
	fi
	go vet ./... || fail=1
	if command -v staticcheck >/dev/null 2>&1; then
		staticcheck ./... || fail=1
	else
		echo "staticcheck: not installed (skipped)"
	fi
done

[ $fail = 0 ] && echo "LINT OK" || echo "LINT FAILED"
exit $fail
