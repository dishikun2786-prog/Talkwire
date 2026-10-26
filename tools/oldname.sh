#!/usr/bin/env bash
# The project was called ImKit until 2026-10-26. The old name stays only where
# something written before the rename is still read: old environment variable
# names, the old migration table, the host tables' rename script, and the notes
# that explain them (CHANGELOG.md). This check keeps it from creeping back
# anywhere else; a new place that really needs it goes on the list below.
set -euo pipefail
cd "$(dirname "$0")/.."

allowed='^(CHANGELOG\.md|README\.md|docs/plan\.md|host-sdk-node/README\.md|host-sdk-node/prisma/rename-from-imkit\.sql|server/config\.go|server/config_env_test\.go|server/migrate/migrate\.go|server/migrate/migrate_test\.go|tools/oldname\.sh)$'

status=0
files=$(git grep -il 'imkit' -- . | grep -Ev "$allowed" || true)
if [ -n "$files" ]; then
	echo "the old name ImKit appears in:"
	echo "$files" | sed 's/^/  /'
	status=1
fi
paths=$(git ls-files | grep -i 'imkit' | grep -Ev "$allowed" || true)
if [ -n "$paths" ]; then
	echo "paths still named after ImKit:"
	echo "$paths" | sed 's/^/  /'
	status=1
fi
[ "$status" = 0 ] && echo "old name only where it is kept on purpose"
exit "$status"
