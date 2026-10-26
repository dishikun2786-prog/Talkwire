#!/bin/sh
# Clears the sandbox: an empty database and no uploaded files, then both
# services again. Installed as /usr/local/sbin/talkwire-sandbox-reset and run
# daily by talkwire-sandbox-reset.timer; safe to run by hand at any time.
#
# The host keeps spaces, users and contacts in memory, so restarting it is the
# other half of the reset. Clients that were connected reconnect, find their
# space gone and are asked to sign in again (the web sample does this).
set -eu

DB=talkwire_sandbox

systemctl stop talkwire-sandbox.service talkwire-im.service

# Each -c runs in its own transaction; DROP DATABASE cannot run inside one.
runuser -u postgres -- psql -v ON_ERROR_STOP=1 -q \
	-c "DROP DATABASE IF EXISTS $DB WITH (FORCE)" \
	-c "CREATE DATABASE $DB OWNER $DB"

rm -rf /var/lib/talkwire-sandbox/media

# The IM server creates its tables on start.
systemctl start talkwire-im.service
systemctl start talkwire-sandbox.service
echo "talkwire sandbox reset at $(date -Is)"
