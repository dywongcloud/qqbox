//go:build windows && amd64

package sandbox

// ptrace is wired and enabled on Windows amd64: the stub publishes the x86-64
// register frame and the dispatch drives the stops (strace-level; single-step
// is not exposed yet).
const ptraceEnabled = true

// x86 can single-step with the trap flag.
const ptraceStepOK = true
