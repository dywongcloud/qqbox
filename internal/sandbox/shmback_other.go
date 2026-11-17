//go:build !windows

package sandbox

// shmBack hands the stub a SysV segment to attach: a stub fd (a /proc-fd
// pairing the stub maps), with the page count in the stub's 16 KiB pages.
func (g *dguest) shmBack(d *dfile, size uint64) (int64, uint64, lxErrno) {
	sfd, e := g.mp.stubFd(g, d)
	if e != 0 {
		return 0, 0, e
	}
	return int64(sfd), pageUp(size) / darwinPage, 0
}
