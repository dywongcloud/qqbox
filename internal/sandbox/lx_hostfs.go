//go:build darwin || linux || windows

package sandbox

import (
	"os"
	"sync/atomic"
	"syscall"
)

// hostStat is a host file's attributes in Linux terms: dev and rdev are Linux
// dev_t encodings and mode carries Linux S_IFMT type bits. Each host builds it
// from its own stat (statOf), so the overlay never looks at a host stat type.
type hostStat struct {
	dev, ino, rdev             uint64
	mode, nlink, uid, gid      uint32
	size, blocks               int64
	blksize                    uint32
	atime, mtime, ctime, btime syscall.Timespec
}

// hostSeq numbers the supervisor's private host objects (work-dir names).
var hostSeq atomic.Uint64

// hostStatfs is the part of a host statfs the guest's statfs reports.
type hostStatfs struct {
	bsize, blocks, bfree, bavail, files, ffree uint64
	fsid                                       [2]uint32
	ftype                                      uint64 // the real backing filesystem's f_type (0 if unknown)
}

// hostFS is the host's file operations on overlay paths. Paths are host paths
// the overlay already resolved: none of their components is a guest symlink,
// and an implementation refuses to follow a host symlink below the layer roots.
// Errors are host errors; osErrno converts them.
type hostFS interface {
	hOpen(p string, flag int, perm os.FileMode) (*os.File, error)
	hLstat(p string) (os.FileInfo, error)
	hReadlink(p string) (string, error)
	hMkdir(p string, perm uint32) error
	hSymlink(target, p string) error
	hMkfifo(p string, perm uint32) error
	hMksock(p string, perm uint32) error
	hRename(from, to string, exchange bool) error
	hUnlink(p string, dir bool) error
	hChmod(p string, mode uint32) error
	hUtimes(p string, atime, mtime syscall.Timespec) error
	hLink(from, to string) error
	hClone(from, to string) error
	hCloneFile(f *os.File, to string) error
	hTruncate(p string, n int64) error
	hStatfs(dir string) (hostStatfs, error)
	hReadDir(p string) ([]os.FileInfo, error)
}
