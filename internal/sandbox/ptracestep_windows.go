//go:build windows

package sandbox

// ptraceInternalPC reports whether pc lies outside the guest's own image
// segments -- a trampoline, stub code or JIT memory -- which a single-step must
// not enter (stepping a trampoline jump would land in the stub and crash). An
// address inside a loaded image segment is guest code and may be stepped.
func (m *memProc) ptraceInternalPC(pc uint64) bool {
	if m.w == nil {
		return true
	}
	for _, s := range m.w.segs {
		if pc >= s.lo && pc < s.hi {
			return false
		}
	}
	return true
}
