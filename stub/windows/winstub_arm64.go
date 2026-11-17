//go:build windows

package winstub

import _ "embed"

// Binary is the arm64 stub executable, built by build.sh.
//
//go:embed stub-arm64.bin
var Binary []byte
