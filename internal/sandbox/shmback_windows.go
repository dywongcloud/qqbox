//go:build windows

package sandbox

import "golang.org/x/sys/windows"

// shmBack hands the stub a SysV segment to attach: a read-write section made
// from the segment's backing file, duplicated into the stub process so the
// stub maps it with NtMapViewOfSectionEx. args[0] carries the section's futex
// identity, as a file mapping does. The page count is in the stub's 4 KiB pages.
func (g *dguest) shmBack(d *dfile, size uint64) (int64, uint64, lxErrno) {
	h := windows.Handle(d.f.Fd())
	sec, err := windows.CreateFileMapping(h, nil, windows.PAGE_READWRITE, 0, 0, nil)
	if err != nil {
		return 0, 0, osErrno(err)
	}
	g.ch.page.args[0] = futexFileKey(h)
	var dup windows.Handle
	err = windows.DuplicateHandle(windows.CurrentProcess(), sec, g.mp.w.sp.proc, &dup, 0, true,
		windows.DUPLICATE_SAME_ACCESS|windows.DUPLICATE_CLOSE_SOURCE)
	if err != nil {
		windows.CloseHandle(sec)
		return 0, 0, osErrno(err)
	}
	return int64(dup), (size + 0xfff) / 0x1000, 0
}
