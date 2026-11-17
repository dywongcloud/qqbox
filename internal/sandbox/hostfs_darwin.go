//go:build darwin

package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Host calls on overlay paths. The guest resolves its own symlinks, so a host
// path under the root or the upper layer never needs a symlink followed. The
// guest can still swap a directory for a symlink (rename, rmdir+symlink)
// between resolving a path and the host call that uses it, so every such call
// passes Darwin's NOFOLLOW_ANY: the kernel then refuses a symlink anywhere in
// the path instead of leaving the layer through it. Both layer roots are
// canonical (no symlinks), which NOFOLLOW_ANY needs. Without a root the guest
// path is the host path and is followed as given.
const (
	atNofollowAny    = 0x0800
	cloneNofollowAny = 0x0008
)

// hostONonblock is the host open flag for a non-blocking open.
const hostONonblock = unix.O_NONBLOCK

// fifoRendezvous: this backend has real FIFOs, so FIFO opens rendezvous.
const fifoRendezvous = true

// darwinFS is the darwin hostFS.
type darwinFS struct{ rootAbs, workAbs string }

func newHostFS(rootAbs, upperAbs, workAbs string) hostFS {
	return newCachedFS(&darwinFS{rootAbs, workAbs}, rootAbs, upperAbs)
}

// newRawHostFS is an uncached host backend confined to root (see the linux note).
func newRawHostFS(root string) hostFS { return &darwinFS{rootAbs: root} }

// hostPath is the host path of resolved guest path gp under root.
func hostPath(root, gp string) string { return filepath.Join(root, filepath.FromSlash(gp)) }

func (fs *darwinFS) oAny() int {
	if fs.rootAbs == "" {
		return 0
	}
	return unix.O_NOFOLLOW_ANY
}

func (fs *darwinFS) atAny() int {
	if fs.rootAbs == "" {
		return 0
	}
	return atNofollowAny
}

func (fs *darwinFS) hOpen(p string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(p, flag|fs.oAny(), perm)
}

// hLstat is os.Lstat that refuses a symlink in any component but the last.
func (fs *darwinFS) hLstat(p string) (os.FileInfo, error) {
	fi := &hostInfo{name: filepath.Base(p)}
	if err := unix.Fstatat(unix.AT_FDCWD, p, (*unix.Stat_t)(unsafe.Pointer(&fi.st)), unix.AT_SYMLINK_NOFOLLOW|fs.atAny()); err != nil {
		return nil, &os.PathError{Op: "lstat", Path: p, Err: err}
	}
	return fi, nil
}

// hParent opens p's parent directory for an *at call on its last component.
func (fs *darwinFS) hParent(p string) (*os.File, string, error) {
	d, err := os.OpenFile(filepath.Dir(p), unix.O_SEARCH|unix.O_DIRECTORY|fs.oAny(), 0)
	return d, filepath.Base(p), err
}

func (fs *darwinFS) hReadlink(p string) (string, error) {
	d, base, err := fs.hParent(p)
	if err != nil {
		return "", err
	}
	defer d.Close()
	for n := 256; ; n *= 2 {
		b := make([]byte, n)
		m, err := unix.Readlinkat(int(d.Fd()), base, b)
		if err != nil {
			return "", &os.PathError{Op: "readlink", Path: p, Err: err}
		}
		if m < n {
			return string(b[:m]), nil
		}
	}
}

func (fs *darwinFS) hMkdir(p string, perm uint32) error {
	d, base, err := fs.hParent(p)
	if err != nil {
		return err
	}
	defer d.Close()
	return unix.Mkdirat(int(d.Fd()), base, perm)
}

func (fs *darwinFS) hSymlink(target, p string) error {
	d, base, err := fs.hParent(p)
	if err != nil {
		return err
	}
	defer d.Close()
	return unix.Symlinkat(target, int(d.Fd()), base)
}

// hMkfifo has no mkfifoat to call, so the FIFO is made in the private work dir
// and renamed into place.
func (fs *darwinFS) hMkfifo(p string, perm uint32) error {
	if fs.workAbs == "" {
		return unix.Mkfifo(p, perm)
	}
	tmp := filepath.Join(fs.workAbs, "fifo-"+strconv.FormatUint(hostSeq.Add(1), 10))
	if err := unix.Mkfifo(tmp, perm); err != nil {
		return err
	}
	err := unix.RenameatxNp(unix.AT_FDCWD, tmp, unix.AT_FDCWD, p, unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY)
	if err != nil {
		unix.Unlink(tmp)
	}
	return err
}

// hMksock makes a socket file. Only root may mknod one on macOS, but binding
// a socket leaves the same unbound socket file: one is bound by the file's
// name in its directory, which this thread alone enters for the bind
// (pthread_fchdir_np), so neither sun_path's length nor a rename comes in.
func (fs *darwinFS) hMksock(p string, perm uint32) error {
	d, err := unix.Open(filepath.Dir(p), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|fs.oAny(), 0)
	if err != nil {
		return err
	}
	defer unix.Close(d)
	s, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(s)
	name := filepath.Base(p)
	runtime.LockOSThread()
	if err = threadDir(int32(d)); err == nil {
		err = unix.Bind(s, &unix.SockaddrUnix{Name: name})
		threadDir(-1)
	}
	runtime.UnlockOSThread()
	if err == unix.EADDRINUSE {
		return unix.EEXIST
	}
	if err != nil {
		return err
	}
	return unix.Fchmodat(d, name, perm, unix.AT_SYMLINK_NOFOLLOW)
}

// The libSystem call that gives one thread its own working directory.
var (
	threadDirOnce sync.Once
	threadDirErr  error
	threadFchdir  *ffiCall // pthread_fchdir_np
	threadErrno   *ffiCall // __error
)

// threadDir makes directory fd the calling thread's working directory, or
// with -1 gives it back the process's. The caller holds the thread locked.
func threadDir(fd int32) error {
	threadDirOnce.Do(func() {
		lib, err := ffiOpenLibSystem()
		if err != nil {
			threadDirErr = err
			return
		}
		if threadFchdir, threadDirErr = ffiPrep(lib, "pthread_fchdir_np", ffiS32, ffiS32); threadDirErr != nil {
			return
		}
		threadErrno, threadDirErr = ffiPrep(lib, "__error", ffiPtr)
	})
	if threadDirErr != nil {
		return threadDirErr
	}
	var r int32
	threadFchdir.call(unsafe.Pointer(&r), unsafe.Pointer(&fd))
	if r == 0 {
		return nil
	}
	var e *int32
	threadErrno.call(unsafe.Pointer(&e))
	return unix.Errno(*e)
}

func (fs *darwinFS) hRename(from, to string, exchange bool) error {
	var flags uint32
	if exchange {
		flags = unix.RENAME_SWAP
	}
	if fs.rootAbs != "" {
		flags |= unix.RENAME_NOFOLLOW_ANY
	}
	return unix.RenameatxNp(unix.AT_FDCWD, from, unix.AT_FDCWD, to, flags)
}

func (fs *darwinFS) hUnlink(p string, dir bool) error {
	fl := fs.atAny()
	if dir {
		fl |= unix.AT_REMOVEDIR
	}
	return unix.Unlinkat(unix.AT_FDCWD, p, fl)
}

func (fs *darwinFS) hChmod(p string, mode uint32) error {
	return unix.Fchmodat(unix.AT_FDCWD, p, mode, unix.AT_SYMLINK_NOFOLLOW|fs.atAny())
}

func (fs *darwinFS) hUtimes(p string, atime, mtime syscall.Timespec) error {
	ts := []unix.Timespec{unix.Timespec(atime), unix.Timespec(mtime)}
	return unix.UtimesNanoAt(unix.AT_FDCWD, p, ts, unix.AT_SYMLINK_NOFOLLOW|fs.atAny())
}

// futimesOf sets the open file f's times. Darwin has no futimens in x/sys;
// futimes keeps microseconds.
func futimesOf(f *os.File, atime, mtime syscall.Timespec) error {
	return unix.Futimes(hostFd(f), []unix.Timeval{
		unix.NsecToTimeval(syscall.TimespecToNsec(atime)), unix.NsecToTimeval(syscall.TimespecToNsec(mtime))})
}

func (fs *darwinFS) hCloneFile(f *os.File, to string) error {
	return unix.Fclonefileat(hostFd(f), unix.AT_FDCWD, to, fs.cloneFlags()&^unix.CLONE_NOFOLLOW)
}

func (fs *darwinFS) hLink(from, to string) error {
	return unix.Linkat(unix.AT_FDCWD, from, unix.AT_FDCWD, to, fs.atAny())
}

func (fs *darwinFS) cloneFlags() int {
	if fs.rootAbs == "" {
		return unix.CLONE_NOFOLLOW
	}
	return unix.CLONE_NOFOLLOW | cloneNofollowAny
}

func (fs *darwinFS) hClone(from, to string) error {
	return unix.Clonefileat(unix.AT_FDCWD, from, unix.AT_FDCWD, to, fs.cloneFlags())
}

func (fs *darwinFS) hTruncate(p string, n int64) error {
	f, err := fs.hOpen(p, os.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(n)
}

func (fs *darwinFS) hStatfs(dir string) (hostStatfs, error) {
	d, err := fs.hOpen(dir, unix.O_SEARCH|unix.O_DIRECTORY, 0)
	if err != nil {
		return hostStatfs{}, err
	}
	defer d.Close()
	return fstatfsOf(d)
}

// fstatfsOf is the statfs of the filesystem holding the open file f.
func fstatfsOf(f *os.File) (hostStatfs, error) {
	var s unix.Statfs_t
	if err := unix.Fstatfs(hostFd(f), &s); err != nil {
		return hostStatfs{}, err
	}
	return hostStatfs{
		bsize: uint64(s.Bsize), blocks: s.Blocks, bfree: s.Bfree, bavail: s.Bavail,
		files: s.Files, ffree: s.Ffree, fsid: [2]uint32{uint32(s.Fsid.Val[0]), uint32(s.Fsid.Val[1])},
	}, nil
}

// hReadDir lists a host directory: names and lstat info, the info taken with
// hLstat (os.DirEntry.Info would re-walk the path and follow a symlink).
func (fs *darwinFS) hReadDir(p string) ([]os.FileInfo, error) {
	f, err := fs.hOpen(p, os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	out := make([]os.FileInfo, 0, len(names))
	for _, n := range names {
		if fi, err := fs.hLstat(p + "/" + n); err == nil {
			out = append(out, fi)
		}
	}
	return out, nil
}

// hostInfo is the os.FileInfo of an hLstat, built like the os package's own.
// statOf is fi's hostStat when fi describes a host file.
func statOf(fi os.FileInfo) (hostStat, bool) {
	if h, ok := fi.Sys().(*hostStat); ok {
		return *h, true // a node-backed FileInfo (lstatGuest via the Namespace)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return hostStat{}, false
	}
	// Darwin's S_IFMT type bits are Linux's.
	return hostStat{
		dev: darwinRdev(uint64(uint32(st.Dev))), ino: st.Ino, rdev: darwinRdev(uint64(uint32(st.Rdev))),
		mode: uint32(st.Mode), nlink: uint32(st.Nlink), uid: st.Uid, gid: st.Gid,
		size: st.Size, blocks: st.Blocks, blksize: uint32(st.Blksize),
		atime: st.Atimespec, mtime: st.Mtimespec, ctime: st.Ctimespec, btime: st.Birthtimespec,
	}, true
}

type hostInfo struct {
	name string
	st   syscall.Stat_t
}

func (h *hostInfo) Name() string       { return h.name }
func (h *hostInfo) Size() int64        { return h.st.Size }
func (h *hostInfo) ModTime() time.Time { return timespecTime(h.st.Mtimespec) }
func (h *hostInfo) IsDir() bool        { return h.Mode().IsDir() }
func (h *hostInfo) Sys() any           { return &h.st }

func (h *hostInfo) Mode() os.FileMode {
	m := os.FileMode(h.st.Mode & 0o777)
	switch h.st.Mode & syscall.S_IFMT {
	case syscall.S_IFBLK, syscall.S_IFWHT:
		m |= os.ModeDevice
	case syscall.S_IFCHR:
		m |= os.ModeDevice | os.ModeCharDevice
	case syscall.S_IFDIR:
		m |= os.ModeDir
	case syscall.S_IFIFO:
		m |= os.ModeNamedPipe
	case syscall.S_IFLNK:
		m |= os.ModeSymlink
	case syscall.S_IFSOCK:
		m |= os.ModeSocket
	}
	if h.st.Mode&syscall.S_ISGID != 0 {
		m |= os.ModeSetgid
	}
	if h.st.Mode&syscall.S_ISUID != 0 {
		m |= os.ModeSetuid
	}
	if h.st.Mode&syscall.S_ISVTX != 0 {
		m |= os.ModeSticky
	}
	return m
}
