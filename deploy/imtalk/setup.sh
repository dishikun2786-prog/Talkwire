#!/bin/bash
# One-time setup of the sandbox on an Ubuntu 24.04 server that already serves
# the website (deploy/site). Run as root from the directory holding this file
# and the units next to it; running it again changes nothing that is in place.
#
#   PostgreSQL 16 with a role and database "talkwire_sandbox"
#   Node 24 (official build, checksum verified) as /usr/local/bin/node
#   /etc/talkwire/im.env and sandbox.env with generated secrets (root only;
#     never printed; kept on later runs)
#   the systemd units, the reset script and its daily timer
#   /etc/caddy/sites/imtalk.caddy
#   a 1 GB swap file when the machine has no swap (the IM server, the host,
#     PostgreSQL and Caddy share 1.6 GB on the first server)
#
# Releases are put in place by release.sh; nothing starts until the first one.
set -euo pipefail
cd "$(dirname "$0")"

[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }

echo "== packages"
export DEBIAN_FRONTEND=noninteractive
if ! command -v psql >/dev/null; then
	apt-get update -q
	apt-get install -y -q postgresql
fi
systemctl enable --now postgresql >/dev/null

echo "== swap"
if [ "$(swapon --noheadings | wc -l)" = 0 ] && [ ! -e /swapfile ]; then
	fallocate -l 1G /swapfile
	chmod 600 /swapfile
	mkswap /swapfile >/dev/null
	swapon /swapfile
	grep -q '^/swapfile ' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi

echo "== node"
if ! /usr/local/bin/node --version 2>/dev/null | grep -q '^v24\.'; then
	tmp=$(mktemp -d)
	base=https://nodejs.org/dist/latest-v24.x
	curl -fsSL "$base/SHASUMS256.txt" -o "$tmp/SHASUMS256.txt"
	file=$(grep -o 'node-v24\.[0-9.]*-linux-x64\.tar\.xz' "$tmp/SHASUMS256.txt" | head -1)
	curl -fsSL "$base/$file" -o "$tmp/$file"
	(cd "$tmp" && grep " $file\$" SHASUMS256.txt | sha256sum -c --quiet)
	rm -rf /opt/node
	mkdir -p /opt/node
	tar -xJf "$tmp/$file" -C /opt/node --strip-components=1
	ln -sfn /opt/node/bin/node /usr/local/bin/node
	rm -rf "$tmp"
fi
/usr/local/bin/node --version

echo "== secrets"
install -d -m 700 /etc/talkwire
if [ ! -f /etc/talkwire/im.env ]; then
	db_password=$(openssl rand -hex 32)
	jwt_secret=$(openssl rand -hex 32)
	umask 077
	cat > /etc/talkwire/im.env <<EOF
IM_PORT=6062
DB_HOST=127.0.0.1
DB_PORT=5432
DB_USER=talkwire_sandbox
DB_PASSWORD=$db_password
DB_NAME=talkwire_sandbox
DB_SSLMODE=disable
JWT_SECRET=$jwt_secret
TALKWIRE_HOST_MODE=http
MAIN_API_BASE_URL=http://127.0.0.1:6001
TALKWIRE_PROTOCOL_PROFILE=strict
IM_MAX_CONNECTIONS=2000
EOF
	cat > /etc/talkwire/sandbox.env <<EOF
JWT_SECRET=$jwt_secret
SANDBOX_RESET_TIME=04:00 Asia/Shanghai
SANDBOX_DOCS_URL=https://talkwire.openedskill.com/guide/sandbox
EOF
	umask 022
fi

echo "== database"
db_password=$(sed -n 's/^DB_PASSWORD=//p' /etc/talkwire/im.env)
if ! runuser -u postgres -- psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='talkwire_sandbox'" | grep -q 1; then
	# Through stdin, so the password is in no process's arguments.
	printf "CREATE ROLE talkwire_sandbox LOGIN PASSWORD '%s';\n" "$db_password" | runuser -u postgres -- psql -q -v ON_ERROR_STOP=1
fi
if ! runuser -u postgres -- psql -tAc "SELECT 1 FROM pg_database WHERE datname='talkwire_sandbox'" | grep -q 1; then
	runuser -u postgres -- psql -q -v ON_ERROR_STOP=1 -c "CREATE DATABASE talkwire_sandbox OWNER talkwire_sandbox"
fi
unset db_password

echo "== directories"
install -d -m 755 /opt/talkwire/releases /var/www/imtalk/releases /etc/caddy/sites

echo "== units"
install -m 644 talkwire-im.service talkwire-sandbox.service talkwire-sandbox-reset.service talkwire-sandbox-reset.timer /etc/systemd/system/
install -m 755 talkwire-sandbox-reset.sh /usr/local/sbin/talkwire-sandbox-reset
systemctl daemon-reload
systemctl enable talkwire-im.service talkwire-sandbox.service >/dev/null
systemctl enable --now talkwire-sandbox-reset.timer >/dev/null

echo "== caddy"
install -m 644 imtalk.caddy /etc/caddy/sites/imtalk.caddy
if ! grep -q '^import /etc/caddy/sites/\*\.caddy' /etc/caddy/Caddyfile; then
	printf '\n# Other sites on this server (deploy/site/Caddyfile).\nimport /etc/caddy/sites/*.caddy\n' >> /etc/caddy/Caddyfile
fi
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null
systemctl reload caddy

echo "setup done; now run release.sh from a checkout"
