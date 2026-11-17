//go:build windows

package winstub

import _ "embed"

// Binary is the x86-64 stub executable, built by build.sh.
//
//go:embed stub-amd64.bin
var Binary []byte
