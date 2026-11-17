//go:build darwin

package sandbox

// macOS is arm64, which cannot single-step from EL0.
const ptraceStepOK = false

func (m *memProc) ptraceInternalPC(uint64) bool { return false }
