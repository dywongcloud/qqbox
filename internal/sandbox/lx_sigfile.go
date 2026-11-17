//go:build darwin || linux || windows

package sandbox

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// signalfd and pidfd. Both are guest fds whose readiness lives on a host pipe
// (read end = the dfile's f, as for an eventfd), so epoll and poll watch them
// like any pipe: a signalfd's pipe holds a byte while a signal in its mask is
// pending for a process holding it (or any of that process's threads), a
// pidfd's once its process has exited. Readiness is shared by every holder, so
// it can be reported to a thread that then finds nothing of its own to read.

const (
	sfdNonblock   = 0x800
	sfdCloexec    = 0x80000
	sigfdInfoSize = 128
)

type sigFile struct {
	pollNote
	sr    *sigRun
	pidfd bool
	mask  atomic.Uint64
	tgid  int // pidfd: the process it names
	r, w  *os.File

	holders map[int]bool // signalfd: tgids that may hold it; guarded by sr.mu

	mu    sync.Mutex
	armed bool
}

func init() {
	syscalls[lxSignalfd4] = sysSignalfd4
}

func (sr *sigRun) fileList() []*sigFile {
	out := make([]*sigFile, 0, len(sr.files))
	for sf := range sr.files {
		out = append(out, sf)
	}
	return out
}

func (sr *sigRun) newSigFile(g *dguest, gp string, pidfd bool, tgid int, nonblock, cloexec bool) (int64, *sigFile) {
	r, w, err := os.Pipe()
	if err != nil {
		return sErr(osErrno(err)), nil
	}
	sf := &sigFile{pollNote: g.pollNote(), sr: sr, pidfd: pidfd, tgid: tgid, r: r, w: w, holders: map[int]bool{g.tgid: true}}
	d := &dfile{f: r, gp: gp, stub: -1, sf: sf}
	d.nonblock.Store(nonblock)
	fd := g.ft.add(d, 0)
	if cloexec {
		g.ft.setCloexec(fd, true)
	}
	sr.mu.Lock()
	sr.files[sf] = struct{}{}
	sr.mu.Unlock()
	return int64(fd), sf
}

// close drops the file with its last descriptor (dfile.unref).
func (sf *sigFile) close() {
	sf.sr.mu.Lock()
	delete(sf.sr.files, sf)
	sf.sr.mu.Unlock()
	sf.w.Close()
}

func (sf *sigFile) setArmed(on bool) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	if on == sf.armed {
		return
	}
	if on {
		sf.w.Write([]byte{1})
	} else {
		var b [1]byte
		readNonblock(sf.r, b[:])
	}
	sf.armed = on
	sf.touch()
}

// refreshFiles re-evaluates every signalfd's readiness after a pending set
// changed, and ends the waits on pendingChanged.
func (sr *sigRun) refreshFiles() {
	sr.mu.Lock()
	if sr.changed != nil {
		close(sr.changed)
		sr.changed = nil
	}
	if len(sr.files) == 0 {
		sr.mu.Unlock()
		return
	}
	type want struct {
		sf   *sigFile
		recs []int // pending-set offsets of every holder process and thread
	}
	var ws []want
	for sf := range sr.files {
		if sf.pidfd {
			continue
		}
		w := want{sf: sf}
		for tgid := range sf.holders {
			p := sr.procs[tgid]
			if p == nil {
				delete(sf.holders, tgid)
				continue
			}
			w.recs = append(w.recs, pOff(p.slot)+spPending)
			for _, t := range p.threads {
				w.recs = append(w.recs, tOff(t.slot)+stPending)
			}
		}
		ws = append(ws, w)
	}
	sr.mu.Unlock()
	for _, w := range ws {
		var pend uint64
		for _, o := range w.recs {
			pend |= sr.ld(o)
		}
		w.sf.setArmed(pend&w.sf.mask.Load() != 0)
	}
}

// sigFileFork records that a forked child holds its parent's signalfds.
func (sr *sigRun) sigFileFork(child *dguest) {
	var fs []*sigFile
	child.ft.mu.Lock()
	for _, d := range child.ft.fds {
		if d.sf != nil && !d.sf.pidfd {
			fs = append(fs, d.sf)
		}
	}
	child.ft.mu.Unlock()
	sr.mu.Lock()
	for _, sf := range fs {
		sf.holders[child.pid] = true
	}
	sr.mu.Unlock()
}

func sysSignalfd4(g *dguest, a [6]uint64) int64 {
	fd, flags := int64(int32(a[0])), a[3]
	if a[2] != 8 || flags&^uint64(sfdNonblock|sfdCloexec) != 0 {
		return sErr(lxEINVAL)
	}
	b, err := g.readMem(a[1], 8)
	if err != nil {
		return sErr(lxEFAULT)
	}
	mask := le64(b) &^ killStopMask
	sr := g.sigs()
	if fd != -1 {
		d, e := g.file(fd)
		if e != 0 {
			return sErr(e)
		}
		if d.sf == nil || d.sf.pidfd {
			return sErr(lxEINVAL)
		}
		d.sf.mask.Store(mask)
		sr.refreshFiles()
		return fd
	}
	nfd, sf := sr.newSigFile(g, "anon_inode:[signalfd]", false, g.tgid, flags&sfdNonblock != 0, flags&sfdCloexec != 0)
	if sf != nil {
		sf.mask.Store(mask)
		sr.refreshFiles()
	}
	return nfd
}

// sigFileRead reads a signalfd: as many signalfd_siginfo records as fit, each
// dequeuing a pending signal in the mask (the reading thread's own first, as
// Linux does). A pidfd is not readable.
func (g *dguest) sigFileRead(d *dfile, bufAddr uint64, n int64) int64 {
	sf := d.sf
	if sf.pidfd || n < sigfdInfoSize {
		return sErr(lxEINVAL)
	}
	sr := g.sigs()
	t := sr.thread(g.pid)
	for {
		changed := sr.pendingChanged()
		var out []byte
		for int64(len(out))+sigfdInfoSize <= n {
			s, si := g.dequeue(sf.mask.Load())
			if s == 0 {
				break
			}
			out = append(out, sigfdInfo(s, si)...)
		}
		sr.refreshFiles()
		if len(out) > 0 {
			if err := g.writeMem(bufAddr, out); err != nil {
				return sErr(lxEFAULT)
			}
			return int64(len(out))
		}
		if d.nonblock.Load() {
			return sErr(lxEAGAIN)
		}
		// Nothing of its own to read, whatever the pipe says (another
		// thread's pending signal arms it too): wait for a signal to be sent.
		if !g.awaitChange(t, changed) {
			return sErr(lxEINTR)
		}
		if g.sigIntr() {
			return restartSys
		}
	}
}

// pendingChanged returns a channel closed the next time a pending set may
// have grown (refreshFiles).
func (sr *sigRun) pendingChanged() chan struct{} {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.changed == nil {
		sr.changed = make(chan struct{})
	}
	return sr.changed
}

// awaitChange blocks until changed is closed or t is kicked; false once the
// guest is torn down.
func (g *dguest) awaitChange(t *sigThread, changed chan struct{}) bool {
	var kick chan struct{}
	if t != nil {
		kick = t.kick()
	}
	select {
	case <-changed:
	case <-kick:
	case <-g.ch.done:
		return false
	case <-g.ch.stop:
		return false
	}
	return true
}

// sigfdInfo encodes struct signalfd_siginfo from a siginfo head.
func sigfdInfo(sig int, si sigInfo) []byte {
	b := make([]byte, sigfdInfoSize)
	put32(b[0:], uint32(sig))
	put32(b[4:], uint32(si.errno))
	put32(b[8:], uint32(si.code))
	switch {
	case si.code == siTimer:
		put32(b[24:], uint32(si.a))     // ssi_tid
		put32(b[32:], uint32(si.a>>32)) // ssi_overrun
		put32(b[44:], uint32(si.b))     // ssi_int
		put64(b[48:], si.b)             // ssi_ptr
	case sig == sigCHLD && si.code > 0:
		put32(b[12:], uint32(si.a))
		put32(b[16:], uint32(si.a>>32))
		put32(b[40:], uint32(si.b)) // ssi_status
	case si.code > 0 && si.code < siKernel && isFaultSig(sig):
		put64(b[72:], si.a) // ssi_addr
	default:
		put32(b[12:], uint32(si.a))     // ssi_pid
		put32(b[16:], uint32(si.a>>32)) // ssi_uid
		put32(b[44:], uint32(si.b))
		put64(b[48:], si.b)
	}
	return b
}

func isFaultSig(sig int) bool {
	switch sig {
	case 4, 5, 7, 8, 11: // SIGILL, SIGTRAP, SIGBUS, SIGFPE, SIGSEGV
		return true
	}
	return false
}

func sysPidfdSendSignal(g *dguest, a [6]uint64) int64 {
	if a[3] != 0 {
		return sErr(lxEINVAL)
	}
	pid, e := g.pidfdPid(int64(int32(a[0])))
	if e == lxEBADF {
		pid, e = g.procDirPid(int64(int32(a[0])))
	}
	if e != 0 {
		return sErr(e)
	}
	si := sigInfo{code: siUser, a: uint64(uint32(g.tgid))}
	if a[2] != 0 {
		var en lxErrno
		if si, en = g.queueInfo(pid, a[2]); en != 0 {
			return sErr(en)
		}
	}
	return g.sendProc(pid, int(int32(a[1])), si)
}

// procDirPid accepts what Linux's pidfd_send_signal also takes in place of a
// pidfd: an open /proc/<tgid> directory.
func (g *dguest) procDirPid(fd int64) (int, lxErrno) {
	d, ok := g.ft.get(int(fd))
	if !ok || !d.isDir {
		return 0, lxEBADF
	}
	rest, ok := strings.CutPrefix(d.name(), "/proc/")
	if !ok {
		return 0, lxEBADF
	}
	pid, err := strconv.Atoi(rest)
	if err != nil || pid <= 0 {
		return 0, lxEBADF
	}
	return pid, 0
}
