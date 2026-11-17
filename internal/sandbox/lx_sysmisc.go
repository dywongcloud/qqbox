//go:build darwin || linux || windows

package sandbox

import (
	"strings"
	"sync"
)

// ioprio_set/ioprio_get, syslog (klogctl), sethostname/setdomainname, and
// setting the clock, which a sandbox without CAP_SYS_TIME may not.

const (
	lxIoprioSet     = 30
	lxIoprioGet     = 31
	lxSyslog        = 116
	lxSethostname   = 161
	lxSetdomainname = 162
	lxSettimeofday  = 170
	lxClockSettime  = 112

	ioprioClassNone = 0
	ioprioClassRT   = 1
	ioprioClassBE   = 2
	ioprioClassIdle = 3

	capSysAdmin = 21

	syslogClose      = 0
	syslogOpen       = 1
	syslogReadAll    = 3
	syslogConsoleLvl = 8
	syslogSizeUnread = 9
	syslogSizeBuffer = 10
	syslogBufLen     = 1 << 17

	utsLen = 64
)

// utsState is the sandbox's UTS namespace: the node and domain names that
// uname and /proc/sys/kernel/{hostname,domainname} report.
type utsState struct {
	mu           sync.Mutex
	node, domain string
}

func (u *utsState) get() (node, domain string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	node, domain = u.node, u.domain
	if node == "" {
		node = utsNodename
	}
	if domain == "" {
		domain = utsDomain
	}
	return node, domain
}

func (u *utsState) set(domain bool, name string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if domain {
		u.domain = name
	} else {
		u.node = name
	}
}

// clone copies the names into a private UTS namespace, for unshare/clone
// CLONE_NEWUTS.
func (u *utsState) clone() *utsState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return &utsState{node: u.node, domain: u.domain}
}

// utsNS is the process's UTS namespace, the run's shared one until it entered a
// private namespace with CLONE_NEWUTS.
func (g *dguest) utsNS() *utsState {
	if g.ps != nil && g.ps.uts != nil {
		return g.ps.uts
	}
	return &g.dfs.uts
}

func init() {
	syscalls[lxIoprioSet] = func(g *dguest, a [6]uint64) int64 {
		return g.ioprioSet(int(int32(a[0])), int(int32(a[1])), int(int32(a[2])))
	}
	syscalls[lxIoprioGet] = func(g *dguest, a [6]uint64) int64 { return g.ioprioGet(int(int32(a[0])), int(int32(a[1]))) }
	syscalls[lxSyslog] = func(g *dguest, a [6]uint64) int64 { return g.syslog(int(int32(a[0])), a[1], int64(int32(a[2]))) }
	syscalls[lxSethostname] = func(g *dguest, a [6]uint64) int64 { return g.setUTS(false, a[0], int64(int32(a[1]))) }
	syscalls[lxSetdomainname] = func(g *dguest, a [6]uint64) int64 { return g.setUTS(true, a[0], int64(int32(a[1]))) }
	syscalls[lxSettimeofday] = func(g *dguest, a [6]uint64) int64 { return sErr(lxEPERM) }
	syscalls[lxClockSettime] = func(g *dguest, a [6]uint64) int64 { return sErr(lxEPERM) }
}

// ioprio's IOPRIO_WHO_* are get/setpriority's PRIO_* plus one.
func (g *dguest) ioprioSet(which, who, prio int) int64 {
	class, level := prio>>13, prio&0x1fff
	switch class {
	case ioprioClassRT:
		if !g.ps.cred.capable(capSysNice) && !g.ps.cred.capable(capSysAdmin) {
			return sErr(lxEPERM)
		}
		fallthrough
	case ioprioClassBE:
		if level&7 != level {
			return sErr(lxEINVAL)
		}
	case ioprioClassIdle:
	case ioprioClassNone:
		if level != 0 {
			return sErr(lxEINVAL)
		}
	default:
		return sErr(lxEINVAL)
	}
	fs := g.dfs
	fs.mu.Lock()
	defer fs.mu.Unlock()
	ps, e := g.prioTargets(which-1, who)
	if e != 0 {
		return sErr(e)
	}
	me := &g.ps.cred
	for _, p := range ps {
		if me.euid != 0 && me.euid != p.cred.ruid && me.euid != p.cred.euid && !me.capable(capSysNice) {
			return sErr(lxEPERM)
		}
	}
	for _, p := range ps {
		p.sched.ioprio = prio
	}
	return 0
}

// ioprioGet reports the highest priority among the targets (lowest class,
// then lowest level); an unset one counts as best-effort level 4.
func (g *dguest) ioprioGet(which, who int) int64 {
	fs := g.dfs
	fs.mu.Lock()
	defer fs.mu.Unlock()
	ps, e := g.prioTargets(which-1, who)
	if e != 0 {
		return sErr(e)
	}
	eff := func(p int) int {
		if p == 0 {
			return ioprioClassBE<<13 | 4
		}
		return p
	}
	best := -1
	for _, p := range ps {
		if best < 0 || eff(p.sched.ioprio) < eff(best) {
			best = p.sched.ioprio
		}
	}
	return int64(best)
}

// syslog is the kernel log of a container without CAP_SYSLOG: the log can be
// read (it is empty) and sized, everything else is EPERM.
func (g *dguest) syslog(typ int, buf uint64, n int64) int64 {
	switch typ {
	case syslogClose, syslogOpen:
		return 0
	case syslogReadAll:
		if buf == 0 || n < 0 {
			return sErr(lxEINVAL)
		}
		return 0
	case syslogSizeBuffer:
		return syslogBufLen
	case 2, 4, 5, 6, 7, syslogConsoleLvl, syslogSizeUnread:
		return sErr(lxEPERM)
	}
	return sErr(lxEINVAL)
}

func (g *dguest) setUTS(domain bool, addr uint64, n int64) int64 {
	if !g.ps.cred.capable(capSysAdmin) {
		return sErr(lxEPERM)
	}
	if n < 0 || n > utsLen {
		return sErr(lxEINVAL)
	}
	b, err := g.readMem(addr, int(n))
	if err != nil {
		return sErr(lxEFAULT)
	}
	name := string(b)
	if i := strings.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	g.utsNS().set(domain, name)
	return 0
}

// utsFile is /proc/sys/kernel/hostname or domainname: reads the current name,
// and a write (by root) sets it, up to the first newline.
func utsFile(domain bool) *pnode {
	return &pnode{kind: pFile, perm: 0o644, write: true,
		gen: func(r *dguest) []byte {
			node, dom := r.utsNS().get()
			if domain {
				return []byte(dom + "\n")
			}
			return []byte(node + "\n")
		},
		put: func(w *dguest, b []byte) lxErrno {
			if !w.ps.cred.capable(capSysAdmin) {
				return lxEPERM
			}
			name, _, _ := strings.Cut(string(b), "\n")
			if len(name) > utsLen {
				return lxEINVAL
			}
			w.utsNS().set(domain, name)
			return 0
		}}
}
