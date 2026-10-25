#!/usr/bin/env bash
# Every check the repository has, the way CI runs them (plan 9.2). CI calls
# this script job by job (.github/workflows/ci.yml), so a check that passes
# here passes there, and the other way round.
#
#   tools/ci.sh                  # every job
#   tools/ci.sh go web           # some of them
#   tools/ci.sh --list           # the jobs and their steps
#
# Jobs:
#   deps         dependency direction and licenses, protocol schemas, red lines
#   go           lint (gofmt, vet, staticcheck), Go tests, race detector
#   conformance  golden transcripts against the server (legacy and strict
#                profiles, and with the Node host SDK as the host), coverage;
#                the table-structure comparison too when the reference server
#                has been built
#   node         Node host SDK (build, types, tests) and the examples
#   web          web SDK: generated types up to date, types, Vitest
#   e2e          Playwright against the sample stack
#   android      Android unit tests, and no libsignal or RTC SDK in the
#                resolved graph of the core modules
#
# What the tests need from the environment:
#   IMKIT_TEST_DSN  libpq key=value of a throwaway PostgreSQL database (tests
#                   drop and recreate schemas in it). Its host, port, user and
#                   dbname also go to the conformance harness, which takes the
#                   password from PGPASSWORD or the pgpass file.
#   DATABASE_URL    a second throwaway database for the host SDK's Prisma
#                   tests; its tables are created with prisma db push.
# Without them the database tests skip, and this script says so. In CI
# (CI=true) a missing one is a failure: there nothing may skip silently.
#
# Neither value is printed. The output of a failing step is printed; it is what
# the tools print, which does not include passwords.
#
# npm packages: with CI=true every directory is installed with npm ci;
# otherwise only where node_modules is missing (npm install).

set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root" || exit 2

ALL_JOBS=(deps go conformance node web e2e android)

case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*) exe=.exe ;;
*) exe= ;;
esac
npm=npm
npx=npx
if [ -n "$exe" ] && command -v npm.cmd >/dev/null 2>&1; then
	npm=npm.cmd
	npx=npx.cmd
fi

# CI_LOGS=<dir> keeps every step's output there (CI uploads it on failure).
if [ -n "${CI_LOGS:-}" ]; then
	mkdir -p "$CI_LOGS" && logs="$(cd "$CI_LOGS" && pwd)"
else
	logs="$(mktemp -d)"
	trap 'rm -rf "$logs"' EXIT
fi
results=()
failed=0

# step NAME COMMAND...: runs it in a subshell, keeps its output, records the result.
step() {
	local name="$1"
	shift
	local log
	log="$logs/$(printf '%s' "$name" | tr -c 'A-Za-z0-9' '_').log"
	local start=$SECONDS
	printf '  %-58s' "$name"
	if ("$@") >"$log" 2>&1; then
		printf 'ok (%ss)\n' $((SECONDS - start))
		results+=("PASS  $name")
	else
		printf 'FAILED (%ss)\n' $((SECONDS - start))
		results+=("FAIL  $name")
		failed=1
		echo "----- $name: last 60 lines"
		tail -n 60 "$log"
		echo "-----"
	fi
}

# skip NAME REASON: a check that cannot run here. In CI that is a failure.
skip() {
	if [ "${CI:-}" = true ]; then
		printf '  %-58s%s\n' "$1" "FAILED (in CI: $2)"
		results+=("FAIL  $1 (in CI: $2)")
		failed=1
	else
		printf '  %-58s%s\n' "$1" "skipped: $2"
		results+=("SKIP  $1: $2")
	fi
}

# note NAME REASON: skipped everywhere, CI included, for a reason outside this repository.
note() {
	printf '  %-58s%s\n' "$1" "skipped: $2"
	results+=("SKIP  $1: $2")
}

need_db() {
	if [ -z "${IMKIT_TEST_DSN:-}" ]; then
		skip "$1" "IMKIT_TEST_DSN not set"
		return 1
	fi
}

# One value of the DSN (key=value pairs, values may be single-quoted). Only
# host, port, user and dbname are ever asked for; the password is not read.
dsn_get() {
	printf '%s' "${IMKIT_TEST_DSN:-}" | tr ' ' '\n' | sed -n "s/^$1=//p" | head -n 1 | tr -d "'"
}

# The conformance harness's database flags, from the DSN (libpq defaults where it says nothing).
pg_flags() {
	local host port user
	host="$(dsn_get host)"
	port="$(dsn_get port)"
	user="$(dsn_get user)"
	printf -- '-pghost %s -pgport %s -pguser %s -db %s' "${host:-127.0.0.1}" "${port:-5432}" "${user:-postgres}" "$(dsn_get dbname)"
}

npm_deps() {
	local dir="$1"
	if [ "${CI:-}" = true ]; then
		(cd "$dir" && $npm ci --no-audit --no-fund)
	elif [ ! -d "$dir/node_modules" ]; then
		(cd "$dir" && $npm install --no-audit --no-fund)
	fi
}

# Built once per run. Steps run in subshells, so "done" is a file, not a variable.
once() {
	local mark="$logs/$1.done"
	shift
	[ -e "$mark" ] && return 0
	"$@" && touch "$mark"
}
go_build() { (cd "$1" && go build -o "$2" "$3"); }
build_server() { once server go_build server "bin/imkit-server$exe" .; }
build_replay() { once replay go_build tools/conformance "bin/replay$exe" ./cmd/replay; }
build_conformance() { once conformance go_build tools/conformance "bin/conformance$exe" ./cmd/conformance; }
host_sdk() { npm_deps host-sdk-node && (cd host-sdk-node && $npm run build); }
build_host_sdk() { once host-sdk host_sdk; }

in_dir() { (cd "$1" && shift && "$@"); }

# ------------------------------------------------------------------ jobs

job_deps() {
	step "host-sdk-node: install (ajv, license walk)" npm_deps host-sdk-node
	step "depcheck: direction and licenses" node tools/depcheck/check.mjs
	step "schema: generated JSON Schema up to date" python -I tools/schema/gen.py --check
	step "schema: every golden frame validates" node tools/schema/check.mjs
	step "red lines: matrix up to date, tests registered" in_dir tools/conformance go run ./cmd/redlines check
}

job_go() {
	step "lint: gofmt, vet, staticcheck" bash tools/lint.sh
	[ -n "${IMKIT_TEST_DSN:-}" ] || skip "server: database tests" "IMKIT_TEST_DSN not set"
	step "server: go test" in_dir server go test -count=1 ./...
	step "tools/conformance: go test" in_dir tools/conformance go test -count=1 ./...
	step "tools/loadtest: go test" in_dir tools/loadtest go test -count=1 ./...
	step "server: race detector" in_dir server bash ../tools/race.sh -count=1 ./...
}

job_conformance() {
	need_db "conformance: golden transcripts" || return 0
	local pg c bin
	pg="$(pg_flags)"
	c="./bin/conformance$exe"
	bin="../../server/bin/imkit-server$exe"
	step "server: build" build_server
	step "conformance: build" build_conformance
	# $pg is deliberately unquoted below: it is several flags.
	step "conformance: coverage" in_dir tools/conformance "$c" coverage
	step "conformance: legacy profile, frame for frame" in_dir tools/conformance "$c" check -bin "$bin" $pg
	step "conformance: strict profile" in_dir tools/conformance "$c" check -bin "$bin" $pg \
		-additions strict -overlay ../../contract/protocol/transcripts-strict -env IMKIT_PROTOCOL_PROFILE=strict
	step "host-sdk-node: install" npm_deps host-sdk-node
	step "conformance: Node host SDK as the host" host_conformance "$pg"
	if [ -x "tools/conformance/oracle/bin/im-oracle$exe" ]; then
		step "conformance: table structure vs. the reference server" in_dir tools/conformance "$c" schema -bin "$bin" $pg \
			-oracle "oracle/bin/im-oracle$exe" -oracle-db "${CONFORMANCE_ORACLE_DB:-imkit_oracle}"
	else
		# Built from the source project by oracle/build-oracle.sh; that source is not in this repository.
		note "conformance: table structure vs. the reference server" "reference server not built"
	fi
}

# The golden transcripts with host-sdk-node's conformance host in place of the
# fake host (in-memory stores). The secret is the harness's published test secret.
host_conformance() {
	local port="${CONFORMANCE_HOST_PORT:-17101}"
	local log="$logs/conformance-host.log"
	(cd host-sdk-node && IMKIT_HOST_SECRET=imkit-conformance-secret-not-for-production-0123456789 \
		exec node --import tsx conformance/host.ts --port "$port" --store memory) >"$log" 2>&1 &
	local pid=$!
	local up=
	for _ in $(seq 1 60); do
		if node -e "fetch('http://127.0.0.1:$port/').then(()=>process.exit(0),()=>process.exit(1))" 2>/dev/null; then
			up=1
			break
		fi
		sleep 0.5
	done
	if [ -z "$up" ]; then
		echo "the conformance host did not start:"
		tail -n 30 "$log"
		kill "$pid" 2>/dev/null
		return 1
	fi
	# $1 is several flags.
	(cd tools/conformance && "./bin/conformance$exe" check -bin "../../server/bin/imkit-server$exe" $1 -host "http://127.0.0.1:$port")
	local rc=$?
	kill "$pid" 2>/dev/null
	wait "$pid" 2>/dev/null
	return $rc
}

job_node() {
	step "host-sdk-node: install and build" build_host_sdk
	step "host-sdk-node: types" in_dir host-sdk-node $npm run typecheck
	if [ -n "${DATABASE_URL:-}" ]; then
		step "host-sdk-node: prisma db push" in_dir host-sdk-node $npx prisma db push --schema prisma --skip-generate
	else
		skip "host-sdk-node: Prisma tests" "DATABASE_URL not set"
	fi
	if [ -n "${IMKIT_TEST_DSN:-}" ]; then
		step "server: build" build_server
	else
		skip "host-sdk-node: tests against the IM server" "IMKIT_TEST_DSN not set"
	fi
	step "host-sdk-node: tests" in_dir host-sdk-node $npm test
	local ex
	for ex in sample-host red-packet artc; do
		step "examples/$ex: install" npm_deps "examples/$ex"
		step "examples/$ex: types" in_dir "examples/$ex" $npm run typecheck
		step "examples/$ex: tests" in_dir "examples/$ex" $npm test
	done
}

job_web() {
	step "web: install" npm_deps web
	step "web: generated frame types up to date" in_dir web $npm run gen:check
	step "web: types (packages, sample, e2e)" in_dir web $npm run typecheck
	step "replay server: build" build_replay
	if [ -n "${IMKIT_TEST_DSN:-}" ]; then
		step "server: build" build_server
	else
		skip "web: tests against a real server" "IMKIT_TEST_DSN not set"
	fi
	step "web: Vitest" in_dir web $npm test
}

job_e2e() {
	need_db "web: end-to-end (Playwright)" || return 0
	step "server: build" build_server
	step "host-sdk-node: install and build" build_host_sdk
	step "examples/sample-host: install" npm_deps examples/sample-host
	step "web: install" npm_deps web
	# Locally the installed Edge (see playwright.config.ts). E2E_CHANNEL= (set, empty) means
	# Playwright's own Chromium; CI sets it, and gets the browser here.
	if [ -n "${E2E_CHANNEL+set}" ] && [ -z "$E2E_CHANNEL" ] && [ "${CI:-}" = true ]; then
		step "web: Playwright Chromium" in_dir web $npx playwright install --with-deps chromium
	fi
	step "web: Playwright" in_dir web $npx playwright test -c e2e
}

job_android() {
	step "replay server: build" build_replay
	if [ -n "${IMKIT_TEST_DSN:-}" ]; then
		step "server: build" build_server
		# SampleStackTest runs the sample host with Node; without its packages it skips.
		step "host-sdk-node: install and build" build_host_sdk
		step "examples/sample-host: install" npm_deps examples/sample-host
	else
		skip "android: tests against a real server" "IMKIT_TEST_DSN not set"
	fi
	step "android: unit tests" in_dir android ./gradlew --no-daemon :imkit-core:test :imkit-storage-room:testDebugUnitTest \
		:imkit-android:testDebugUnitTest :imkit-push-fcm:testDebugUnitTest :imkit-ui:testDebugUnitTest :example-red-packet:testDebugUnitTest
	# depcheck reads the declared dependencies; this is the resolved graph, transitive ones included.
	step "android: no libsignal or RTC SDK in the core graph" android_graph
}

android_graph() {
	local out hits
	out="$(cd android && ./gradlew --no-daemon -q :imkit-ui:dependencies --configuration releaseRuntimeClasspath)" || return 1
	hits="$(printf '%s\n' "$out" | grep -Ei 'libsignal|org\.signal|dingrtc|com\.ding\.rtc|alivc' || true)"
	if [ -n "$hits" ]; then
		echo "forbidden in :imkit-ui releaseRuntimeClasspath:"
		echo "$hits"
		return 1
	fi
	if ! printf '%s\n' "$out" | grep -q 'imkit-core'; then
		echo "the graph does not list :imkit-core; is the configuration name right?"
		return 1
	fi
	echo "$(printf '%s\n' "$out" | grep -c -- '--- ') entries, none forbidden"
}

# ------------------------------------------------------------------ main

if [ "${1:-}" = --list ]; then
	for j in "${ALL_JOBS[@]}"; do
		echo "$j"
		declare -f "job_$j" | sed -n 's/^ *\(step\|skip\|note\|need_db\) "\([^"]*\)".*/    \2/p; s/^ *\[.*\] || skip "\([^"]*\)".*/    \1/p'
	done
	exit 0
fi

jobs=("$@")
[ ${#jobs[@]} -eq 0 ] && jobs=("${ALL_JOBS[@]}")
for j in "${jobs[@]}"; do
	if ! declare -F "job_$j" >/dev/null; then
		echo "unknown job: $j (jobs: ${ALL_JOBS[*]})" >&2
		exit 2
	fi
done

for j in "${jobs[@]}"; do
	echo "== $j"
	"job_$j"
done

echo
echo "== summary"
printf '%s\n' "${results[@]}"
echo "passed $(printf '%s\n' "${results[@]}" | grep -c '^PASS'), failed $(printf '%s\n' "${results[@]}" | grep -c '^FAIL'), skipped $(printf '%s\n' "${results[@]}" | grep -c '^SKIP')"
exit $failed
