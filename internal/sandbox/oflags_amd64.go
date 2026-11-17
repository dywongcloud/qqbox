//go:build darwin || linux || windows

package sandbox

// oflagsToGeneric maps x86-64 open flags to the shared table's generic values.
// open and openat get this swap through toGeneric/x64Generic on their register
// argument; openat2's flags come from a struct in guest memory, so its handler
// calls this directly. See swapOFlags and x64OFlags in lx_x64nr_amd64.go.
func oflagsToGeneric(f uint64) uint64 { return swapOFlags(f, true) }
