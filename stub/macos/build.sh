#!/bin/sh
# Builds the macOS (arm64) stub, embedded by the sandbox darwin backend. Cross
# -compiles from Linux with the pinned Zig toolchain; the result is ad-hoc
# codesigned at runtime by the supervisor before exec (arm64 macOS refuses an
# unsigned freshly written binary).
set -e
cd "$(dirname "$0")"
# The freestanding sources were authored against Zig 0.13.0. Prefer ZIG,
# then the installed zig; a different major version needs a source migration.
ZIG=${ZIG:-$(command -v zig 2>/dev/null || printf %s /tmp/zig-linux-x86_64-0.13.0/zig)}
if ! "$ZIG" version >/dev/null 2>&1; then
    printf '%s\n' 'laboon: no usable zig toolchain; set ZIG=<path>' >&2
    exit 1
fi
case "$("$ZIG" version 2>/dev/null)" in
    0.13.*) ;;
    *) printf '%s\n' "laboon: warning: Zig $("$ZIG" version 2>/dev/null) may not build sources written for 0.13.0" >&2 ;;
esac
"$ZIG" build-exe -target aarch64-macos -lc -O ReleaseSmall \
	-femit-bin=stub.bin stub.zig
rm -f stub.bin.o
