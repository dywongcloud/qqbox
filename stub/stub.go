// Package stub embeds the freestanding process that guest code runs in, one
// build per guest architecture. See zig/stub.zig for the protocol and the
// seccomp policy.
package stub

//go:generate ./build.sh
