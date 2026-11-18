#!/bin/sh
# Builds the Windows stubs, embedded by the sandbox windows backend:
# stub-amd64.bin (x86-64) and stub-arm64.bin (arm64). Cross-compiles from
# Linux with the pinned Zig toolchain. ntdll is the only import;
# wWinMainCRTStartup is ours (no CRT, no kernel32).
#
#   build.sh [amd64|arm64]   one of them; both by default
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
build() {
	"$ZIG" build-exe -target "$1"-windows-gnu -mcpu=baseline -O ReleaseSmall \
		-fentry=wWinMainCRTStartup --subsystem console --image-base 0x140000000 -fno-unwind-tables -fno-compiler-rt -lntdll \
		-femit-bin="stub-$2.bin" stub.zig
	rm -f "stub-$2.bin.o" "stub-$2.bin.obj" "stub-$2.pdb" "stub-$2.lib"
}
case ${1:-all} in
amd64 | all) build x86_64 amd64 ;;
esac
case ${1:-all} in
arm64 | all) build aarch64 arm64 ;;
esac
