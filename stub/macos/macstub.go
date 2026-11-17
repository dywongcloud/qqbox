// Package macstub embeds the macOS (arm64) stub executable that a Linux guest
// runs in. See stub.zig for the protocol, the loader, the trampoline and the
// Seatbelt backstop; build.sh produces stub.bin. The darwin sandbox backend
// writes this to a temp file, ad-hoc codesigns it and execs it per guest.
package macstub

import _ "embed"

//go:generate ./build.sh
//go:generate ./build-hvf.sh

// Binary is the stub executable, built by build.sh.
//
//go:embed stub.bin
var Binary []byte

// HvfBinary is the HVF-backend stub, built by build-hvf.sh. It runs a non-PIE
// low-linked ET_EXEC guest in a VM (Hypervisor.framework) rather than mapping
// and rewriting it, so there is no __PAGEZERO wall and no Seatbelt allowlist
// (the VM is the boundary). The backend signs it with the hypervisor entitlement.
//
//go:embed hvf_stub.bin
var HvfBinary []byte
