#!/bin/sh
# Fail when a function is reachable from neither a main path nor a test.
#
# golangci-lint's `unused` deliberately skips exported identifiers, so an
# exported helper nothing calls stays invisible to it. `deadcode` builds a call
# graph instead and closes that gap. Both are needed.
#
# The -test flag counts tests as entry points, so this reports only code that
# is dead for real. Without -test the same command also lists test-only code,
# which is a useful review signal but not a gate.
set -eu

if ! command -v deadcode >/dev/null 2>&1; then
	echo "installing deadcode..." >&2
	go install golang.org/x/tools/cmd/deadcode@latest
fi

out=$(deadcode -test ./...)

if [ -n "$out" ]; then
	echo "$out"
	echo >&2
	echo "unreachable from any main path or test: wire it up or delete it." >&2
	exit 1
fi
