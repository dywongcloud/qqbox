//go:build darwin || linux || windows

package sandbox

// lxRestartSyscall is restart_syscall, which a stub issues in place of a call
// that returned ERESTART_RESTARTBLOCK when no handler ran: it resumes what the
// call left in g.restart, and fails with EINTR when there is nothing.
const lxRestartSyscall = 128

func init() {
	syscalls[lxRestartSyscall] = func(g *dguest, a [6]uint64) int64 {
		fn := g.restart
		g.restart = nil
		if fn == nil {
			return sErr(lxEINTR)
		}
		return fn()
	}
}
