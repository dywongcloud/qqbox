//go:build darwin || linux || windows

package sandbox

// oflagsToGeneric is identity on arm64: its open flags already are the generic
// (asm-generic) values the shared table uses.
func oflagsToGeneric(f uint64) uint64 { return f }
