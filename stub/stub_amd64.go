package stub

import _ "embed"

// Binary is the top-level stub, an ET_EXEC at the primary window, built by
// build.sh.
//
//go:embed stub-amd64.bin
var Binary []byte

// BinaryPIE is the nested stub, an ET_DYN carrying the window note; a qqbox
// running inside qqbox launches it and its outer places it at a free high
// window. See build.sh and stub/zig/stub.zig.
//
//go:embed stub-amd64-pie.bin
var BinaryPIE []byte
