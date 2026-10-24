#!/usr/bin/env bash
# Run Go tests under the race detector.
#
#   tools/race.sh                 # go test -race ./... in the current module
#   tools/race.sh -run TestFoo ./replay/
#
# Arguments are passed to "go test -race" unchanged (default: ./...).
#
# The race detector needs cgo and a C compiler. On Linux and macOS the system
# compiler is used as is. On Windows this script uses a private MinGW-w64 GCC
# and turns cgo on for this one command only: nothing is added to the system
# or user PATH and no "go env -w" is written, because a gcc on PATH would make
# every other Go build on the machine switch to cgo silently.
#
# Windows needs two workarounds, both confirmed on Windows 11 (26200) with
# Go 1.27.0 and WinLibs GCC 16.2.0:
#
#  1. Paths with spaces. GCC's built-in link rule adds the default manifest
#     through %:if-exists(default-manifest.o%s), whose result is split on
#     spaces, so a toolchain under "Program Files" fails to link ("cannot find
#     F:/Program"). A one-rule specs override replaces it with a plain
#     default-manifest.o%s lookup (the manifest is still linked in). The rule
#     is taken from this gcc's own -dumpspecs, so it follows the gcc version.
#     An 8.3 short path does not help: gcc resolves its own location to the
#     long path.
#  2. ASLR. The linker's default --dynamicbase makes ThreadSanitizer fail at
#     start ("failed to allocate ... error code: 87"). Test binaries are
#     linked with --disable-dynamicbase and --disable-high-entropy-va.
#
# The toolchain location can be overridden with IMKIT_MINGW (the directory
# that holds bin/gcc.exe).
set -euo pipefail

if [ "$#" -eq 0 ]; then
	set -- ./...
fi

case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*) ;;
*)
	exec env CGO_ENABLED=1 go test -race "$@"
	;;
esac

mingw="${IMKIT_MINGW:-/f/Program Files/tools/winlibs/mingw64}"
gcc="$mingw/bin/gcc.exe"
if [ ! -x "$gcc" ]; then
	echo "race.sh: no gcc at $gcc" >&2
	echo "race.sh: install WinLibs GCC (MinGW-w64, UCRT, POSIX threads) there, or set IMKIT_MINGW" >&2
	exit 2
fi

# The specs file itself must sit on a path without spaces: -specs= is split
# like any other flag in CGO_LDFLAGS.
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
specs="$tmp/imkit-endfile.specs"
{
	echo '*endfile:'
	"$gcc" -dumpspecs | awk '/^\*endfile:/ { getline; print; exit }' |
		sed 's/%:if-exists(default-manifest\.o%s)/default-manifest.o%s/'
	echo
} >"$specs"
if ! grep -q 'default-manifest\.o%s' "$specs"; then
	echo "race.sh: this gcc has no default-manifest rule to override; check $specs" >&2
	exit 2
fi
specs_win="$(cygpath -m "$specs")"
case "$specs_win" in
*" "*)
	echo "race.sh: temp directory has a space ($specs_win); set TMPDIR to a path without spaces" >&2
	exit 2
	;;
esac

export PATH="$mingw/bin:$PATH"
export CGO_ENABLED=1
export CGO_LDFLAGS="${CGO_LDFLAGS:--O2 -g} -specs=$specs_win -Wl,--disable-dynamicbase,--disable-high-entropy-va"
go test -race "$@"
