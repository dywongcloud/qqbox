//go:build windows && arm64

package sandbox

// ptrace is wired on Windows arm64: the stub publishes the arm64 register
// frame and the dispatch drives the stops (strace-level; arm64 cannot
// single-step from EL0).
const ptraceEnabled = true

// arm64 cannot single-step from EL0.
const ptraceStepOK = false
