package sandbox

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Host calls on overlay paths. The guest resolves its own symlinks, so a host
// path under the root or the upper layer never needs one followed; the guest
// can still swap a directory for a symlink between resolving a path and the
// host call that uses it. So every call reaches its last component through
// the parent directory, opened with openat2's RESOLVE_NO_SYMLINKS, and acts
// on it without following: the kernel refuses a symlink anywhere on the way
// instead of leaving the layer through it. Without a root the guest path is
// the host path and is followed as given.

// hostONonblock is the host open flag for a non-blocking open.
const hostONonblock = unix.O_NONBLOCK

// fifoRendezvous: this backend has real FIFOs, so FIFO opens rendezvous.
const fifoRendezvous = true

type linuxFS struct{ rootAbs string }

func newHostFS(rootAbs, upperAbs, workAbs string) hostFS {
	return newCachedFS(&linuxFS{rootAbs}, rootAbs, upperAbs)
}

// newRawHostFS is a host backend confined to root but without the overlay cache,
// for a writable bind whose directory changes under us (the cache assumes an
// immutable lower layer).
func newRawHostFS(root string) hostFS { return &linuxFS{root} }

// hostPath is the host path of resolved guest path gp under root.
func hostPath(root, gp string) string { return filepath.Join(root, filepath.FromSlash(gp)) }

func (fs *linuxFS) resolve() uint64 {
	if fs.rootAbs == "" {
		return 0
	}
	return unix.RESOLVE_NO_SYMLINKS
}

func (fs *linuxFS) open2(p string, flags int, perm uint32) (int, error) {
	return unix.Openat2(unix.AT_FDCWD, p, &unix.OpenHow{Flags: uint64(flags | unix.O_CLOEXEC), Mode: uint64(perm), Resolve: fs.resolve()})
}

func (fs *linuxFS) hOpen(p string, flag int, perm os.FileMode) (*os.File, error) {
	fd, err := fs.open2(p, flag, uint32(perm.Perm()))
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	return os.NewFile(uintptr(fd), p), nil
}

// hParent opens p's parent directory for an *at call on its last component.
func (fs *linuxFS) hParent(p string) (int, string, error) {
	fd, err := fs.open2(filepath.Dir(p), unix.O_PATH|unix.O_DIRECTORY, 0)
	return fd, filepath.Base(p), err
}

// atParent runs call on p's parent directory and last component.
func (fs *linuxFS) atParent(p string, call func(dir int, base string) error) error {
	d, base, err := fs.hParent(p)
	if err != nil {
		return err
	}
	defer unix.Close(d)
	return call(d, base)
}

func (fs *linuxFS) hLstat(p string) (os.FileInfo, error) {
	fi := &hostInfo{name: filepath.Base(p)}
	err := fs.atParent(p, func(d int, base string) error {
		return unix.Fstatat(d, base, (*unix.Stat_t)(unsafe.Pointer(&fi.st)), unix.AT_SYMLINK_NOFOLLOW)
	})
	if err != nil {
		return nil, &os.PathError{Op: "lstat", Path: p, Err: err}
	}
	return fi, nil
}

func (fs *linuxFS) hReadlink(p string) (string, error) {
	var s string
	err := fs.atParent(p, func(d int, base string) error {
		for n := 256; ; n *= 2 {
			b := make([]byte, n)
			m, err := unix.Readlinkat(d, base, b)
			if err != nil {
				return err
			}
			if m < n {
				s = string(b[:m])
				return nil
			}
		}
	})
	if err != nil {
		return "", &os.PathError{Op: "readlink", Path: p, Err: err}
	}
	return s, nil
}

func (fs *linuxFS) hMkdir(p string, perm uint32) error {
	return fs.atParent(p, func(d int, base string) error { return unix.Mkdirat(d, base, perm) })
}

func (fs *linuxFS) hSymlink(target, p string) error {
	return fs.atParent(p, func(d int, base string) error { return unix.Symlinkat(target, d, base) })
}

func (fs *linuxFS) hMkfifo(p string, perm uint32) error {
	return fs.atParent(p, func(d int, base string) error { return unix.Mkfifoat(d, base, perm) })
}

func (fs *linuxFS) hMksock(p string, perm uint32) error {
	return fs.atParent(p, func(d int, base string) error { return unix.Mknodat(d, base, unix.S_IFSOCK|perm, 0) })
}

func (fs *linuxFS) hRename(from, to string, exchange bool) error {
	var flags uint
	if exchange {
		flags = unix.RENAME_EXCHANGE
	}
	return fs.atParent(from, func(fd int, fbase string) error {
		return fs.atParent(to, func(td int, tbase string) error {
			return unix.Renameat2(fd, fbase, td, tbase, flags)
		})
	})
}

func (fs *linuxFS) hUnlink(p string, dir bool) error {
	fl := 0
	if dir {
		fl = unix.AT_REMOVEDIR
	}
	return fs.atParent(p, func(d int, base string) error { return unix.Unlinkat(d, base, fl) })
}

// hChmod changes p's mode through a descriptor of p itself: Linux's fchmodat
// cannot leave a final symlink unfollowed, and a symlink's mode is not used.
func (fs *linuxFS) hChmod(p string, mode uint32) error {
	fd, err := fs.open2(p, unix.O_PATH|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return nil
	}
	return unix.Fchmodat(unix.AT_FDCWD, procFdPath(fd), mode, 0)
}

func procFdPath(fd int) string { return "/proc/self/fd/" + strconv.Itoa(fd) }

func (fs *linuxFS) hUtimes(p string, atime, mtime syscall.Timespec) error {
	ts := []unix.Timespec{unix.Timespec(atime), unix.Timespec(mtime)}
	return fs.atParent(p, func(d int, base string) error {
		return unix.UtimesNanoAt(d, base, ts, unix.AT_SYMLINK_NOFOLLOW)
	})
}

// futimesOf sets the open file f's times.
func futimesOf(f *os.File, atime, mtime syscall.Timespec) error {
	return unix.UtimesNanoAt(hostFd(f), "", []unix.Timespec{unix.Timespec(atime), unix.Timespec(mtime)}, unix.AT_EMPTY_PATH)
}

func (fs *linuxFS) hLink(from, to string) error {
	return fs.atParent(from, func(fd int, fbase string) error {
		return fs.atParent(to, func(td int, tbase string) error {
			return unix.Linkat(fd, fbase, td, tbase, 0)
		})
	})
}

// hClone copies from to the new file to, sharing blocks where the filesystem
// can (FICLONE), with its mode.
func (fs *linuxFS) hClone(from, to string) error {
	f, err := fs.hOpen(from, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return fs.hCloneFile(f, to)
}

func (fs *linuxFS) hCloneFile(f *os.File, to string) error {
	var st unix.Stat_t
	if err := unix.Fstat(hostFd(f), &st); err != nil {
		return err
	}
	out, err := fs.hOpen(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, os.FileMode(st.Mode&0o7777))
	if err != nil {
		return err
	}
	if unix.IoctlFileClone(hostFd(out), hostFd(f)) != nil {
		if _, err = io.Copy(out, io.NewSectionReader(f, 0, st.Size)); err != nil {
			out.Close()
			fs.hUnlink(to, false)
			return err
		}
	}
	unix.Fchmod(hostFd(out), st.Mode&0o7777)
	return out.Close()
}

func (fs *linuxFS) hTruncate(p string, n int64) error {
	f, err := fs.hOpen(p, os.O_WRONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(n)
}

func (fs *linuxFS) hStatfs(dir string) (hostStatfs, error) {
	d, err := fs.hOpen(dir, unix.O_RDONLY|unix.O_DIRECTORY, 0)
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
		ftype: uint64(s.Type),
	}, nil
}

// hReadDir lists a host directory from one getdents64 stream: each entry's name,
// inode (d_ino) and type (d_type) come straight from the kernel, so there is no
// per-entry stat. An entry whose d_type is DT_UNKNOWN (rare -- some filesystems
// omit it) is stat'd on its own to learn its type.
func (fs *linuxFS) hReadDir(p string) ([]os.FileInfo, error) {
	f, err := fs.hOpen(p, os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fd := int(hostFd(f))
	var out []os.FileInfo
	buf := make([]byte, 16<<10)
	for {
		n, err := unix.Getdents(fd, buf)
		if err != nil {
			return nil, err
		}
		if n <= 0 {
			break
		}
		for off := 0; off+19 <= n; {
			// struct linux_dirent64: d_ino(8) d_off(8) d_reclen(2) d_type(1) name.
			reclen := int(binary.LittleEndian.Uint16(buf[off+16:]))
			if reclen < 19 || off+reclen > n {
				break
			}
			ino := binary.LittleEndian.Uint64(buf[off:])
			dtype := buf[off+18]
			name := direntName(buf[off+19 : off+reclen])
			off += reclen
			if name == "" || name == "." || name == ".." {
				continue
			}
			fi := &hostInfo{name: name}
			fi.st.Ino = ino
			if m := modeFromDType(dtype); m != 0 {
				fi.st.Mode = m
			} else if unix.Fstatat(fd, name, (*unix.Stat_t)(unsafe.Pointer(&fi.st)), unix.AT_SYMLINK_NOFOLLOW) != nil {
				continue
			}
			out = append(out, fi)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].(*hostInfo).name < out[j].(*hostInfo).name })
	return out, nil
}

// direntName returns the NUL-terminated name from a linux_dirent64 name field.
func direntName(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// modeFromDType maps a getdents64 d_type to the S_IFMT type bits of st_mode, or
// 0 for DT_UNKNOWN (the caller then stats the entry).
func modeFromDType(dt byte) uint32 {
	switch dt {
	case unix.DT_REG:
		return syscall.S_IFREG
	case unix.DT_DIR:
		return syscall.S_IFDIR
	case unix.DT_LNK:
		return syscall.S_IFLNK
	case unix.DT_CHR:
		return syscall.S_IFCHR
	case unix.DT_BLK:
		return syscall.S_IFBLK
	case unix.DT_FIFO:
		return syscall.S_IFIFO
	case unix.DT_SOCK:
		return syscall.S_IFSOCK
	default:
		return 0
	}
}

// statOf is fi's hostStat when fi describes a host file.
func statOf(fi os.FileInfo) (hostStat, bool) {
	if h, ok := fi.Sys().(*hostStat); ok {
		return *h, true // a node-backed FileInfo (lstatGuest via the Namespace)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return hostStat{}, false
	}
	return hostStat{
		dev: st.Dev, ino: st.Ino, rdev: st.Rdev,
		mode: st.Mode, nlink: uint32(st.Nlink), uid: st.Uid, gid: st.Gid,
		size: st.Size, blocks: st.Blocks, blksize: uint32(st.Blksize),
		atime: st.Atim, mtime: st.Mtim, ctime: st.Ctim,
	}, true
}

// hostInfo is the os.FileInfo of an hLstat, built like the os package's own.
type hostInfo struct {
	name string
	st   syscall.Stat_t
}

func (h *hostInfo) Name() string       { return h.name }
func (h *hostInfo) Size() int64        { return h.st.Size }
func (h *hostInfo) ModTime() time.Time { return time.Unix(h.st.Mtim.Unix()) }
func (h *hostInfo) IsDir() bool        { return h.Mode().IsDir() }
func (h *hostInfo) Sys() any           { return &h.st }

func (h *hostInfo) Mode() os.FileMode {
	m := os.FileMode(h.st.Mode & 0o777)
	switch h.st.Mode & syscall.S_IFMT {
	case syscall.S_IFBLK:
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
