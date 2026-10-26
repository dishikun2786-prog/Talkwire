#!/bin/bash
# Builds the sandbox and puts it on the server: the IM server (Linux binary),
# the sandbox host (examples/sample-host/src/sandbox-main.ts bundled into one
# file with esbuild, so the server needs Node and nothing from npm) and the
# web sample. Then switches "current" and restarts both services.
#
#   deploy/imtalk/release.sh [root@43.103.5.167]
#
# Needs Go, Node and the installed node_modules of web/, host-sdk-node/ and
# examples/sample-host/ (npm ci in each). Run setup.sh on the server first.
set -euo pipefail

TARGET=${1:-root@43.103.5.167}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
OUT="$ROOT/deploy/imtalk/.build"
SHA=$(git -C "$ROOT" rev-parse --short=12 HEAD)
[ -z "$(git -C "$ROOT" status --porcelain)" ] || SHA="$SHA-dirty"
NAME="$(date -u +%Y%m%d%H%M%S)-$SHA"

rm -rf "$OUT"
mkdir -p "$OUT/app"

echo "== IM server"
(cd "$ROOT/server" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OUT/app/talkwire-server" .)

echo "== sandbox host"
(cd "$ROOT/host-sdk-node" && npm run build --silent)
(cd "$ROOT/examples/sample-host" && ./node_modules/.bin/esbuild src/sandbox-main.ts \
	--bundle --platform=node --format=esm --target=node22 --log-level=warning \
	"--banner:js=import { createRequire as __cr } from 'node:module'; const require = __cr(import.meta.url);" \
	--outfile="$OUT/app/sandbox.mjs")

echo "== web sample"
(cd "$ROOT/web" && npm run build --silent && npx vite build sample --config sample/vite.config.ts --outDir "$OUT/web" --emptyOutDir --logLevel warn)

echo "== upload $NAME"
# --force-local: GNU tar on Windows reads "C:" as a remote host otherwise.
tar --force-local -czf "$OUT/release.tgz" -C "$OUT" app web
scp -q "$OUT/release.tgz" "$TARGET:/tmp/talkwire-release-$NAME.tgz"

ssh "$TARGET" NAME="$NAME" bash -s <<'REMOTE'
set -euo pipefail
tmp=$(mktemp -d)
tar -xzf "/tmp/talkwire-release-$NAME.tgz" -C "$tmp"
rm -f "/tmp/talkwire-release-$NAME.tgz"
mv "$tmp/app" "/opt/talkwire/releases/$NAME"
mv "$tmp/web" "/var/www/imtalk/releases/$NAME"
rmdir "$tmp"
chmod -R a+rX "/opt/talkwire/releases/$NAME" "/var/www/imtalk/releases/$NAME"
chmod 755 "/opt/talkwire/releases/$NAME/talkwire-server"
# Atomic switch: a new symlink renamed over the old one.
for d in /opt/talkwire /var/www/imtalk; do
	ln -sfn "releases/$NAME" "$d/current.new"
	mv -Tf "$d/current.new" "$d/current"
done
systemctl restart talkwire-im.service
systemctl restart talkwire-sandbox.service
for i in $(seq 1 30); do
	if curl -fsS http://127.0.0.1:6062/api/health >/dev/null 2>&1 && curl -fsS http://127.0.0.1:6001/sandbox/info >/dev/null 2>&1; then
		echo "healthy: $NAME"
		# Keep the last five releases of each.
		for d in /opt/talkwire/releases /var/www/imtalk/releases; do
			ls -1d "$d"/*/ | sort | head -n -5 | xargs -r rm -rf
		done
		exit 0
	fi
	sleep 1
done
echo "not healthy after 30 s" >&2
journalctl -u talkwire-im -u talkwire-sandbox -n 40 --no-pager >&2
exit 1
REMOTE
