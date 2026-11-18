//go:build darwin || linux

package sandbox

import (
	"os"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Guest terminals: /dev/ptmx, /dev/pts/N and /dev/tty. Each is a host terminal
// the supervisor opens: /dev/ptmx a host pty master, /dev/pts/N the host slave
// /dev/ttysNNN (N is the host's pty index, so names agree both ways), /dev/tty
// the supervisor's controlling terminal. Their line discipline is the host's.

// ptmx is the state of a guest pty master: its index and Linux's slave lock,
// kept here (the host slave is unlocked at once, since Darwin cannot re-lock).
//
// Until the guest first opens the slave the supervisor holds it open: a
// Darwin master whose slave was never opened rejects the terminal ioctls and
// blocks writes, where Linux serves and buffers them. The guest's first open
// releases it, so the master still sees the hangup when the guest's last
// slave fd closes.
type ptmx struct {
	ts     *termState
	n      int
	slave  string // host path of the slave
	locked bool
	hold   *os.File
}

// termState is a run's terminal state; it lives on the dfs so it dies with
// the run. ptmx are the pty masters this run created: their slaves, and the
// run's own stdio terminal, are the only host terminals a guest may open.
type termState struct {
	mu   sync.Mutex
	ptmx map[int]*ptmx
	ctl  map[uint64]*ttyCtl // controlling terminals by host rdev
}

// close releases what the guest left open when the run ends.
func (ts *termState) close() {
	ts.mu.Lock()
	ps := ts.ptmx
	ts.ptmx = nil
	ts.mu.Unlock()
	for _, p := range ps {
		p.release()
	}
}

// openTTY opens the guest terminal device gp, reporting false when gp is not
// one of them.
func (g *dguest) openTTY(gp string, flags int64) (int64, bool) {
	switch {
	case gp == "/dev/ptmx":
		return g.openPtmx(flags), true
	case gp == "/dev/tty":
		ctty := g.cttyPath()
		if ctty == "" {
			return sErr(lxENXIO), true
		}
		return g.openTTYPath(ctty, flags), true
	case strings.HasPrefix(gp, "/dev/pts/"):
		n, ok := g.ptsVisible(gp)
		if !ok {
			return sErr(lxENOENT), true
		}
		g.term.mu.Lock()
		p := g.term.ptmx[n]
		locked := p != nil && p.locked
		g.term.mu.Unlock()
		if locked {
			return sErr(lxEIO), true
		}
		fd := g.openTTYPath(hostPtsPath(n), flags)
		if p != nil && fd >= 0 {
			p.release()
		}
		return fd, true
	}
	return 0, false
}

// ptsVisible parses a guest /dev/pts/N and reports whether the guest may see
// it: a slave of a ptmx this run created, or the run's stdio terminal. Other
// host terminals belong to other sessions of the user.
func (g *dguest) ptsVisible(gp string) (int, bool) {
	s := strings.TrimPrefix(gp, "/dev/pts/")
	if s == "" || len(s) > 6 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	g.term.mu.Lock()
	_, mine := g.term.ptmx[n]
	g.term.mu.Unlock()
	if mine {
		return n, true
	}
	if m, ok := hostPtsNum(g.cttyPath()); ok && m == n {
		return n, true
	}
	return 0, false
}

// cttyPath is the host path of the guest's controlling terminal: the run's
// stdio terminal (the host's /dev/tty is a different device node, so its fds
// would not match the terminal's).
func (g *dguest) cttyPath() string {
	for _, r := range []any{g.cfg.Stdin, g.cfg.Stdout, g.cfg.Stderr} {
		if f, ok := r.(*os.File); ok && f != nil && isHostTTY(hostFd(f)) {
			if p, err := hostTTYName(hostFd(f)); err == nil {
				return p
			}
		}
	}
	return ""
}

// ttyLstat stats a guest terminal path on the host.
func (g *dguest) ttyLstat(gp string) (os.FileInfo, lxErrno, bool) {
	var host string
	switch {
	case gp == "/dev/ptmx", gp == "/dev/tty":
		host = gp
	case gp == "/dev/pts":
		host = "/dev"
	case strings.HasPrefix(gp, "/dev/pts/"):
		n, ok := g.ptsVisible(gp)
		if !ok {
			return nil, lxENOENT, true
		}
		host = hostPtsPath(n)
	default:
		return nil, 0, false
	}
	fi, err := os.Stat(host)
	if err != nil {
		return nil, osErrno(err), true
	}
	return fi, 0, true
}

// openTTYPath opens a host terminal (never as the supervisor's controlling
// terminal) and installs it as a guest fd.
func (g *dguest) openTTYPath(host string, flags int64) int64 {
	f, err := os.OpenFile(host, goOpenFlags(flags)&^os.O_CREATE|syscall.O_NOCTTY, 0)
	if err != nil {
		return sErr(osErrno(err))
	}
	return g.installTTY(&dfile{f: f, gp: guestTTYName(host), stub: -1, tty: true}, flags)
}

func (g *dguest) installTTY(d *dfile, flags int64) int64 {
	d.nonblock.Store(flags&lxONonblock != 0)
	d.fx.oflags, d.fx.hasFlags = flags&lxOStatusKeep|lxOLargefile, true
	fd := g.ft.add(d, 0)
	if flags&lxOCloexec != 0 {
		g.ft.setCloexec(fd, true)
	}
	return int64(fd)
}

// openPtmx creates a pty pair on the host and returns its master. As on Linux
// the slave starts locked until TIOCSPTLCK clears it (unlockpt).
func (g *dguest) openPtmx(flags int64) int64 {
	f, slave, e := hostPtyOpen()
	if e != 0 {
		return sErr(e)
	}
	n, ok := hostPtsNum(slave)
	if !ok {
		f.Close()
		return sErr(lxEIO)
	}
	hold, err := os.OpenFile(slave, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		f.Close()
		return sErr(osErrno(err))
	}
	p := &ptmx{ts: &g.term, n: n, slave: slave, locked: true, hold: hold}
	g.term.mu.Lock()
	if g.term.ptmx == nil {
		g.term.ptmx = map[int]*ptmx{}
	}
	g.term.ptmx[n] = p
	g.term.mu.Unlock()
	return g.installTTY(&dfile{f: f, gp: "/dev/ptmx", stub: -1, tty: true, pty: p}, flags)
}

// close forgets a master once its description is gone.
func (p *ptmx) close() {
	p.release()
	p.ts.mu.Lock()
	if p.ts.ptmx[p.n] == p {
		delete(p.ts.ptmx, p.n)
	}
	p.ts.mu.Unlock()
}

func (p *ptmx) release() {
	p.ts.mu.Lock()
	h := p.hold
	p.hold = nil
	p.ts.mu.Unlock()
	if h != nil {
		h.Close()
	}
}

// ptmxIoctl serves the requests specific to a pty master; the rest (termios,
// window size) fall through to the terminal ones, which act on the pair.
func (g *dguest) ptmxIoctl(d *dfile, req, arg uint64) (int64, bool) {
	p := d.pty
	switch req {
	case lxTIOCPKT:
		// VTE enables packet mode on the master before spawning its shell. The
		// guest pty is a host pty pair, so forward the request to that master:
		// returning success without enabling it leaves VTE reading a stream it
		// cannot interpret, which shows up as a terminal that never paints text.
		b, err := g.readMem(arg, 4)
		if err != nil {
			return sErr(lxEFAULT), true
		}
		on := 0
		if le32(b) != 0 {
			on = 1
		}
		// TIOCPKT takes a pointer to an int, not the int itself (on Darwin the
		// request is 0x80047470, an _IOC_IN with a 4-byte payload). Passing the
		// value directly makes the kernel dereference address 0/1 and VTE sees
		// EFAULT as "Failed to open PTY: Bad address".
		if err := unix.IoctlSetPointerInt(hostFd(d.f), unix.TIOCPKT, on); err != nil {
			return sErr(osErrno(err)), true
		}
		return 0, true
	case lxTIOCGPTN:
		return g.ttyPutU32(arg, uint32(p.n)), true
	case lxTIOCSPTLCK:
		b, err := g.readMem(arg, 4)
		if err != nil {
			return sErr(lxEFAULT), true
		}
		lock := le32(b) != 0
		p.ts.mu.Lock()
		p.locked = lock
		p.ts.mu.Unlock()
		return 0, true
	case lxTIOCGPTLCK:
		p.ts.mu.Lock()
		v := uint32(0)
		if p.locked {
			v = 1
		}
		p.ts.mu.Unlock()
		return g.ttyPutU32(arg, v), true
	case lxTIOCGPTPEER:
		p.ts.mu.Lock()
		locked := p.locked
		p.ts.mu.Unlock()
		if locked {
			return sErr(lxEIO), true
		}
		fd := g.openTTYPath(p.slave, int64(arg))
		if fd >= 0 {
			p.release()
		}
		return fd, true
	}
	return 0, false
}

// termRead and termWrite serve a terminal's IO in the supervisor; darwin's
// terminals are host ones, read and written through their host files.
func (g *dguest) termRead(d *dfile, bufAddr uint64, n int64) (int64, bool) { return 0, false }

func (g *dguest) termWrite(d *dfile, bufAddr uint64, n int64) (int64, bool) { return 0, false }

// ptsDir is /dev/pts as a synthetic directory; ttyLstat serves its entries.
func (g *dguest) ptsDir() *pnode {
	return &pnode{kind: pDir, perm: 0o755, kids: func() []pchild { return nil }}
}
