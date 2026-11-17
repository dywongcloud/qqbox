//go:build darwin || linux || windows

package sandbox

// argRead reads n bytes of a syscall's input at addr: from the copy the stub
// made as the call entered, when it made one, else from guest memory. Only a
// handler reading its own call's inputs uses it.
func (g *dguest) argRead(addr uint64, n int) ([]byte, error) {
	b := make([]byte, n)
	if g.argCopy(addr, b) {
		return b, nil
	}
	return g.readMem(addr, n)
}

// putOut writes b, a syscall's result, to guest memory at addr: left for the
// stub to copy as the call returns when it can be, else now. Only a handler
// writing its own call's results uses it, and only for writes that may land as
// the call returns.
func (g *dguest) putOut(addr uint64, b []byte) error {
	if g.deferOut(addr, b) {
		return nil
	}
	return g.writeMem(addr, b)
}

// argGet fills b with a syscall's input at addr, as argRead does.
func (g *dguest) argGet(addr uint64, b []byte) error {
	if g.argCopy(addr, b) {
		return nil
	}
	return g.getBuf(addr, b)
}
