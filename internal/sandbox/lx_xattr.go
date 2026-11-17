//go:build darwin || linux || windows

package sandbox

// Extended attributes, as on a filesystem that keeps none: a file lists no
// names, and getting, setting or removing one is not supported. Tools that
// copy attributes (cp -a, Python's shutil) take both in their stride.

const (
	lxSetxattr     = 5
	lxLsetxattr    = 6
	lxFsetxattr    = 7
	lxGetxattr     = 8
	lxLgetxattr    = 9
	lxFgetxattr    = 10
	lxListxattr    = 11
	lxLlistxattr   = 12
	lxFlistxattr   = 13
	lxRemovexattr  = 14
	lxLremovexattr = 15
	lxFremovexattr = 16
)

func init() {
	byPath := func(follow, list bool) func(g *dguest, a [6]uint64) int64 {
		return func(g *dguest, a [6]uint64) int64 { return g.xattrPath(a[0], follow, list) }
	}
	byFd := func(list bool) func(g *dguest, a [6]uint64) int64 {
		return func(g *dguest, a [6]uint64) int64 { return g.xattrFd(int64(int32(a[0])), list) }
	}
	for _, nr := range []uint64{lxSetxattr, lxGetxattr, lxRemovexattr} {
		syscalls[nr] = byPath(true, false)
	}
	for _, nr := range []uint64{lxLsetxattr, lxLgetxattr, lxLremovexattr} {
		syscalls[nr] = byPath(false, false)
	}
	for _, nr := range []uint64{lxFsetxattr, lxFgetxattr, lxFremovexattr} {
		syscalls[nr] = byFd(false)
	}
	syscalls[lxListxattr] = byPath(true, true)
	syscalls[lxLlistxattr] = byPath(false, true)
	syscalls[lxFlistxattr] = byFd(true)
}

// xattrPath serves a path-named attribute call: the file must exist; it
// lists nothing, and anything else is unsupported.
func (g *dguest) xattrPath(pathAddr uint64, follow, list bool) int64 {
	gp, e := g.lookup(lxAtFdcwd, pathAddr, follow)
	if e != 0 {
		return sErr(e)
	}
	if _, e := g.lstatGuest(gp); e != 0 {
		return sErr(e)
	}
	if list {
		return 0
	}
	return sErr(lxEOPNOTSUPP)
}

func (g *dguest) xattrFd(fd int64, list bool) int64 {
	if _, ok := g.ft.get(int(fd)); !ok {
		return sErr(lxEBADF)
	}
	if list {
		return 0
	}
	return sErr(lxEOPNOTSUPP)
}
