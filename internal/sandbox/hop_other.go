//go:build darwin || windows

package sandbox

// hopTable is Linux's per-process host-op state (hop_linux.go).
type hopTable struct{}

// endStub is Linux's (hop_linux.go): elsewhere a stub ends with its last thread.
func (fs *dfs) endStub(ps *pstate) {}
