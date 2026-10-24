#!/bin/bash
# Builds the reference ("oracle") IM server from the frozen source commit.
# Reads the source repo only through `git archive`, so the source working tree
# is never touched. Output: oracle/bin/im-oracle(.exe) + oracle/SOURCE.sha256.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
SRC_REPO="${SRC_REPO:-/d/Program Files/roubao}"
COMMIT="${ORACLE_COMMIT:-33e232508821cd44005316299e69a876394baa86}"
rm -rf "$HERE/src" && mkdir -p "$HERE/src" "$HERE/bin"
# core.autocrlf=false: export the bytes Git stores (LF). With the Git for Windows
# default (true) the archive comes out CRLF and the hashes below would only
# match on machines configured the same way.
git -c core.autocrlf=false -C "$SRC_REPO" archive "$COMMIT" server/im-go | tar -x -C "$HERE/src" --exclude='*.exe' --exclude='im-server-linux'
cd "$HERE/src/server/im-go"
( cd "$HERE/src/server/im-go" && sha256sum *.go go.mod go.sum ) > "$HERE/SOURCE.sha256"
GOFLAGS=-mod=readonly go build -o "$HERE/bin/im-oracle$( [ "${OS:-}" = Windows_NT ] && echo .exe )" .
echo "built oracle from $COMMIT"
