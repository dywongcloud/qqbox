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
ZIG=${ZIG:-/tmp/zig-linux-x86_64-0.13.0/zig}
"$ZIG" build-exe -target aarch64-macos -lc -O ReleaseSmall \
	-F hvf-sdk -framework Hypervisor \
	-femit-bin=hvf_stub.bin hvf_stub.zig
rm -f hvf_stub.bin.o
