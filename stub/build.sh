#!/bin/sh
# Builds the freestanding stub for each guest architecture. The stub never
# touches FP/SIMD registers, so the syscall entry need not save the guest's.
#
# Two variants per architecture:
#   - the primary stub: an ET_EXEC linked at STUB_PRIMARY (a fixed high window,
#     clear of guest images). The top-level qqbox launches this one by a real
#     kernel exec, which places ET_EXEC at its fixed address with no collision.
#   - the PIE stub: an ET_DYN carrying the ".note.qqbox" window note. A qqbox
#     running inside qqbox launches this; its outer's loader reads the note and
#     places it at a FREE high window (sandbox/load_linux.go), so nesting
#     composes to any depth -- one relocatable binary per level, not a fixed set
#     of windows. The stub derives its window from its own load base at runtime
#     and applies its own relative relocations (stub.zig), so the loader places
#     it without relocating.
# STUB_BASE (zig/layout.zig) is the primary ET_EXEC base and the first window
# the loader tries for a PIE; it is written per build and restored at the end.
set -e
cd "$(dirname "$0")"
# The freestanding sources were authored against Zig 0.13.0. Prefer ZIG,
# then the installed zig; a different major version needs a source migration.
ZIG=${ZIG:-$(command -v zig 2>/dev/null || printf %s /tmp/zig-linux-x86_64-0.13.0/zig)}
if ! "$ZIG" version >/dev/null 2>&1; then
    printf '%s\n' 'qqbox: no usable zig toolchain; set ZIG=<path>' >&2
    exit 1
fi
case "$("$ZIG" version 2>/dev/null)" in
    0.13.*) ;;
    *) printf '%s\n' "qqbox: warning: Zig $("$ZIG" version 2>/dev/null) may not build sources written for 0.13.0" >&2 ;;
esac

STUB_PRIMARY=0x6f0000000000

common="-fno-unwind-tables -fno-stack-protector -fno-strip"

# exe builds the top-level ET_EXEC at the fixed primary base.
exe() { # arch mcpu out
	printf 'pub const STUB_BASE: u64 = %s;\n' "$STUB_PRIMARY" > zig/layout.zig
	"$ZIG" build-exe -target "$1"-linux-none -mcpu "$2" -O ReleaseSmall -fPIC -fno-PIE \
		$common --image-base "$STUB_PRIMARY" -femit-bin="$3" zig/stub.zig
	"$ZIG" objcopy --strip-debug "$3" "$3".tmp && mv "$3".tmp "$3"
	rm -f "$3".o
}

# pie builds the nested PIE (ET_DYN). STUB_BASE is still the primary -- it is
# only the probe's first window; the PIE's actual base is chosen at load.
pie() { # arch mcpu out
	printf 'pub const STUB_BASE: u64 = %s;\n' "$STUB_PRIMARY" > zig/layout.zig
	"$ZIG" build-exe -target "$1"-linux-none -mcpu "$2" -O ReleaseSmall -fPIE \
		$common -femit-bin="$3" zig/stub.zig
	"$ZIG" objcopy --strip-debug "$3" "$3".tmp && mv "$3".tmp "$3"
	rm -f "$3".o
}

exe x86_64 x86_64-sse-sse2-mmx-x87-avx-avx2+soft_float stub-amd64.bin
pie x86_64 x86_64-sse-sse2-mmx-x87-avx-avx2+soft_float stub-amd64-pie.bin
exe aarch64 generic-fp_armv8-neon stub-arm64.bin
pie aarch64 generic-fp_armv8-neon stub-arm64-pie.bin

printf 'pub const STUB_BASE: u64 = %s;\n' "$STUB_PRIMARY" > zig/layout.zig
