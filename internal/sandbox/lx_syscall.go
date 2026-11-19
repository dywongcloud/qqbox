//go:build darwin || linux || windows

package sandbox

import (
	"fmt"
	"sync"
)

// syscalls holds the handlers for Linux syscalls that are answered by a single
// result, keyed by Linux arm64 (asm-generic) number; an x86-64 guest's numbers
// are translated first. Each subsystem registers its own from an init func in
// its file; a host's serve loop keeps the calls that need the channel itself
// (host round trips, exec, exit, clone).
var syscalls = map[uint64]func(g *dguest, a [6]uint64) int64{}

// sysTable holds the handlers of the Linux numbers, which index it, so a
// call finds its handler without a map lookup; qqbox's own numbers stay in
// syscalls. Built once, after every init has registered its handlers.
var sysTable = sync.OnceValue(func() *[1024]func(g *dguest, a [6]uint64) int64 {
	var t [1024]func(g *dguest, a [6]uint64) int64
	for nr, h := range syscalls {
		if nr < uint64(len(t)) {
			t[nr] = h
		}
	}
	return &t
})

// handler is syscall nr's handler, if it has one.
func handler(nr uint64) (func(g *dguest, a [6]uint64) int64, bool) {
	if t := sysTable(); nr < uint64(len(t)) {
		return t[nr], t[nr] != nil
	}
	h, ok := syscalls[nr]
	return h, ok
}

// Linux arm64 syscall numbers the guest issues.
const (
	lxRead       = 63
	lxWrite      = 64
	lxReadv      = 65
	lxWritev     = 66
	lxExit       = 93
	lxExitGroup  = 94
	lxIoctl      = 29
	lxOpenat     = 56
	lxClose      = 57
	lxLseek      = 62
	lxFstat      = 80
	lxNewfstatat = 79
	lxGetcwd     = 17
	lxMkdirat    = 34
	lxUnlinkat   = 35
	lxGetdents64 = 61
	lxReadlinkat = 78
	lxStatx      = 291
	lxUname      = 160
	lxChroot     = 51
	lxChdir      = 49
	lxFchdir     = 50
	lxDup        = 23
	lxDup3       = 24
	lxFcntl      = 25
	lxFaccessat  = 48
	lxFaccessat2 = 439
	lxPipe2      = 59
	lxClone      = 220
	lxExecve     = 221
	lxWait4      = 260

	lxRtSigaction   = 134
	lxRtSigprocmask = 135
	lxSigaltstack   = 132
	lxRseq          = 293
	lxGetpid        = 172
	lxGetppid       = 173
	lxGettid        = 178
	lxPrlimit64     = 261

	lxEventfd2     = 19
	lxEpollCreate1 = 20
	lxEpollCtl     = 21
	lxEpollPwait   = 22

	lxCloneVM = 0x100 // CLONE_VM set: a thread; clear: a fork
)

// Linux epoll / eventfd constants (arm64).
const (
	lxEpollCtlAdd = 1
	lxEpollCtlDel = 2
	lxEpollCtlMod = 3

	lxEpollIn      = 0x1
	lxEpollOut     = 0x4
	lxEpollErr     = 0x8
	lxEpollHup     = 0x10
	lxEpollRdhup   = 0x2000
	lxEpollOneshot = 1 << 30
	lxEpollEt      = 1 << 31

	lxEfdSemaphore = 1
	lxONonblock    = 0x800
)

// lcRWX is the stub asking whether the run allows writable+executable
// memory, which the stub's own mmap and mprotect serve (stub/macos/mem.zig,
// stub/windows/mem.zig): 0, or -EACCES with Config.RejectRWX.
const lcRWX = 0x4c430030

// Linux mmap protection bits.
const (
	lxProtRead  = 0x1
	lxProtWrite = 0x2
	lxProtExec  = 0x4
)

func errno(e lxErrno) uint64 { return uint64(-int64(e)) }

// regSys registers a syscall handler, panicking if the number is already
// taken. A duplicate means two files claim the same call for one build, which
// map assignment would hide behind init order; the panic surfaces it at
// startup instead.
func regSys(nr uint64, h func(g *dguest, a [6]uint64) int64) {
	if _, dup := syscalls[nr]; dup {
		panic(fmt.Sprintf("sandbox: syscall %d registered twice", nr))
	}
	syscalls[nr] = h
}

func init() {
	syscalls[lcRWX] = func(g *dguest, _ [6]uint64) int64 {
		if g.cfg.RejectRWX {
			return sErr(lxEACCES)
		}
		return 0
	}
	syscalls[lxRead] = func(g *dguest, a [6]uint64) int64 { return g.read(int64(a[0]), a[1], int64(a[2])) }
	syscalls[lxWrite] = func(g *dguest, a [6]uint64) int64 { return g.writeFile(int64(a[0]), a[1], int64(a[2])) }
	syscalls[lxReadv] = func(g *dguest, a [6]uint64) int64 { return g.readv(int64(a[0]), a[1], int64(a[2])) }
	syscalls[lxWritev] = func(g *dguest, a [6]uint64) int64 { return g.writev(int64(a[0]), a[1], int64(a[2])) }
	syscalls[lxPipe2] = func(g *dguest, a [6]uint64) int64 { return g.pipe2(a[0], int64(a[1])) }
	syscalls[lxDup] = func(g *dguest, a [6]uint64) int64 { return g.dup(int64(a[0])) }
	syscalls[lxDup3] = func(g *dguest, a [6]uint64) int64 { return g.dup3(int64(a[0]), int64(a[1]), int64(a[2])) }
	syscalls[lxFcntl] = func(g *dguest, a [6]uint64) int64 { return g.fcntl(int64(a[0]), int64(a[1]), a[2]) }
	syscalls[lxFaccessat] = func(g *dguest, a [6]uint64) int64 { return g.faccessat2(int64(a[0]), a[1], int64(a[2]), 0) }
	syscalls[lxFaccessat2] = func(g *dguest, a [6]uint64) int64 { return g.faccessat2(int64(a[0]), a[1], int64(a[2]), int64(a[3])) }
	syscalls[lxOpenat] = func(g *dguest, a [6]uint64) int64 { return g.openat(int64(a[0]), a[1], int64(a[2]), int64(a[3])) }
	syscalls[lxMkdirat] = func(g *dguest, a [6]uint64) int64 { return g.mkdirat(int64(a[0]), a[1], int64(a[2])) }
	syscalls[lxUnlinkat] = func(g *dguest, a [6]uint64) int64 { return g.unlinkat(int64(a[0]), a[1], int64(a[2])) }
	syscalls[lxClose] = func(g *dguest, a [6]uint64) int64 { return g.closeFd(int64(a[0])) }
	syscalls[lxLseek] = func(g *dguest, a [6]uint64) int64 { return g.lseek(int64(a[0]), int64(a[1]), int64(a[2])) }
	syscalls[lxFstat] = func(g *dguest, a [6]uint64) int64 { return g.fstatFd(int64(a[0]), a[1]) }
	syscalls[lxNewfstatat] = func(g *dguest, a [6]uint64) int64 { return g.newfstatat(int64(a[0]), a[1], a[2], int64(a[3])) }
	syscalls[lxGetcwd] = func(g *dguest, a [6]uint64) int64 { return g.getcwd(a[0], int64(a[1])) }
	syscalls[lxGetdents64] = func(g *dguest, a [6]uint64) int64 { return g.getdents64(int64(a[0]), a[1], int64(a[2])) }
	syscalls[lxReadlinkat] = func(g *dguest, a [6]uint64) int64 { return g.readlinkat(int64(a[0]), a[1], a[2], int64(a[3])) }
	// statx(dirfd, path, flags, mask, buf); mask (a[3]) ignored — we
	// always report STATX_BASIC_STATS.
	syscalls[lxStatx] = func(g *dguest, a [6]uint64) int64 { return g.statx(int64(a[0]), a[1], int64(a[2]), a[4]) }
	syscalls[lxUname] = func(g *dguest, a [6]uint64) int64 { return g.uname(a[0]) }
	syscalls[lxChdir] = func(g *dguest, a [6]uint64) int64 { return g.chdir(a[0]) }
	syscalls[lxFchdir] = func(g *dguest, a [6]uint64) int64 { return g.fchdir(int64(a[0])) }
	syscalls[lxChroot] = func(g *dguest, a [6]uint64) int64 { return g.chroot(a[0]) }
	syscalls[lxIoctl] = func(g *dguest, a [6]uint64) int64 { return g.ioctl(int64(a[0]), a[1], a[2]) }
	syscalls[lxEventfd2] = func(g *dguest, a [6]uint64) int64 { return g.eventfd2(uint64(uint32(a[0])), int64(a[1])) }
	syscalls[lxEpollCreate1] = func(g *dguest, a [6]uint64) int64 { return g.epollCreate1(int64(a[0])) }
	syscalls[lxEpollCtl] = func(g *dguest, a [6]uint64) int64 {
		return g.epollCtl(int64(a[0]), int64(a[1]), int64(int32(a[2])), a[3])
	}
	syscalls[lxEpollPwait] = func(g *dguest, a [6]uint64) int64 { return g.epollPwait(a) }
	syscalls[lxGetpid] = func(g *dguest, a [6]uint64) int64 { return int64(g.nsPid(g.tgid)) }
	syscalls[lxGettid] = func(g *dguest, a [6]uint64) int64 { return int64(g.nsPid(g.pid)) }
	syscalls[lxRseq] = func(g *dguest, a [6]uint64) int64 { return g.rseq(a) }
}
