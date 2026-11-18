#!/bin/sh
# Builds the macOS (arm64) HVF-backend stub: it runs the guest in a VM
# (Hypervisor.framework) instead of mapping and rewriting it, so it also handles
# non-PIE low-linked ET_EXEC (no host __PAGEZERO) and needs no Seatbelt allowlist
# (the VM is the boundary). Cross-compiles from Linux with the pinned Zig
# toolchain against the bundled Hypervisor.framework .tbd stub (hvf-sdk/); the
# real framework is resolved at runtime on the Mac. The supervisor ad-hoc
# codesigns the result WITH the com.apple.security.hypervisor entitlement before
# exec (arm64 macOS refuses an unsigned freshly written binary; HVF needs the
# entitlement).
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
	-F hvf-sdk -framework Hypervisor \
	-femit-bin=hvf_stub.bin hvf_stub.zig
rm -f hvf_stub.bin.o
