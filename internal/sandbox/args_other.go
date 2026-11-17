//go:build darwin || windows

package sandbox

// argSet is Linux's (args_linux.go): these stubs copy no syscall inputs ahead.
type argSet struct{}

func (g *dguest) argCopy(uint64, []byte) bool { return false }

func (g *dguest) argCStr(uint64) (string, bool) { return "", false }

func (g *dguest) deferOut(uint64, []byte) bool { return false }

func (g *dguest) flushOuts() {}
