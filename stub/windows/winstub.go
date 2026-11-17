//go:build windows

// Package winstub embeds the Windows stub executable that a Linux guest runs
// in, for the host's architecture. See stub.zig for the protocol; build.sh
// produces stub-amd64.bin and stub-arm64.bin.
package winstub

//go:generate ./build.sh
