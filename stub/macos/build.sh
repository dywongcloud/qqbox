#!/bin/sh
# Builds the macOS (arm64) stub, embedded by the sandbox darwin backend. Cross
# -compiles from Linux with the pinned Zig toolchain; the result is ad-hoc
# codesigned at runtime by the supervisor before exec (arm64 macOS refuses an
# unsigned freshly written binary).
set -e
cd "$(dirname "$0")"
ZIG=${ZIG:-/tmp/zig-linux-x86_64-0.13.0/zig}
"$ZIG" build-exe -target aarch64-macos -lc -O ReleaseSmall \
	-femit-bin=stub.bin stub.zig
rm -f stub.bin.o
