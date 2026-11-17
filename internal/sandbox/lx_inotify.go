//go:build darwin || linux || windows

package sandbox

import (
	"os"
	"path"
	"strings"
	"sync"
)

// inotify for the macOS backend. Every change a guest makes to the overlay goes
// through a supervisor handler, so events are raised there, the way Linux's
// VFS raises fsnotify events: with child names, rename cookies and self events,
// which a host kqueue EVFILT_VNODE watch could not name. An inotify fd is a
// queue of struct inotify_event records whose readiness is mirrored onto a host
// pipe (one byte while the queue is non-empty), so epoll can watch it. Changes
// made outside the guest (or through a shared mapping) are not seen.

const (
	lxInotifyInit1    = 26
	lxInotifyAddWatch = 27
	lxInotifyRmWatch  = 28

	inAccess     = 0x1
	inModify     = 0x2
	inAttrib     = 0x4
	inCloseWrite = 0x8
	inCloseNow   = 0x10
	inOpen       = 0x20
	inMovedFrom  = 0x40
	inMovedTo    = 0x80
	inCreate     = 0x100
	inDelete     = 0x200
	inDeleteSelf = 0x400
	inMoveSelf   = 0x800
	inAllEvents  = 0xfff

	inUnmount    = 0x2000
	inQOverflow  = 0x4000
	inIgnored    = 0x8000
	inOnlydir    = 0x01000000
	inDontFollow = 0x02000000
	inExclUnlink = 0x04000000
	inMaskCreate = 0x10000000
	inMaskAdd    = 0x20000000
	inIsdir      = 0x40000000
	inOneshot    = 0x80000000

	inQueueMax = 16384
)

type inotify struct {
	pollNote
	mu     sync.Mutex
	cond   *sync.Cond
	q      [][]byte
	w      *os.File // readiness pipe write end (the dfile's f is the read end)
	ready  bool
	closed bool
	next   int32
	watch  map[int32]*iwatch
	fs     *dfs
	dn     *dnotify // set for an F_NOTIFY instance, which signals instead of queueing
	dnFire bool
}

type iwatch struct {
	wd   int32
	gp   string
	mask uint32
}

func init() {
	syscalls[lxInotifyInit1] = func(g *dguest, a [6]uint64) int64 { return g.inotifyInit(int64(a[0])) }
	syscalls[lxInotifyAddWatch] = func(g *dguest, a [6]uint64) int64 {
		return g.inotifyAddWatch(int64(a[0]), a[1], uint32(a[2]))
	}
	syscalls[lxInotifyRmWatch] = func(g *dguest, a [6]uint64) int64 { return g.inotifyRmWatch(int64(a[0]), int32(a[1])) }
}

func (g *dguest) inotifyInit(flags int64) int64 {
	if flags&^(lxOCloexec|lxONonblock) != 0 {
		return sErr(lxEINVAL)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return sErr(osErrno(err))
	}
	in := &inotify{pollNote: g.pollNote(), w: w, watch: map[int32]*iwatch{}, fs: g.dfs, next: 1}
	in.cond = sync.NewCond(&in.mu)
	d := &dfile{f: r, gp: "anon_inode:inotify", stub: -1}
	d.fx.in = in
	g.ov.inotifyMu.Lock()
	g.ov.watchers = append(g.ov.watchers, in)
	g.ov.inotifyMu.Unlock()
	return g.installD(d, flags|lxORdonly)
}

func (g *dguest) inotifyOf(fd int64) (*inotify, lxErrno) {
	d, ok := g.ft.get(int(fd))
	if !ok {
		return nil, lxEBADF
	}
	if d.fx.in == nil {
		return nil, lxEINVAL
	}
	return d.fx.in, 0
}

func (g *dguest) inotifyAddWatch(fd int64, pathAddr uint64, mask uint32) int64 {
	in, e := g.inotifyOf(fd)
	if e != 0 {
		return sErr(e)
	}
	if mask&inAllEvents == 0 || mask&inMaskAdd != 0 && mask&inMaskCreate != 0 {
		return sErr(lxEINVAL)
	}
	gp, e := g.lookup(lxAtFdcwd, pathAddr, mask&inDontFollow == 0)
	if e != 0 {
		return sErr(e)
	}
	fi, e := g.lstatGuest(gp)
	if e != 0 {
		return sErr(e)
	}
	if mask&inOnlydir != 0 && !fi.IsDir() {
		return sErr(lxENOTDIR)
	}
	events := mask & (inAllEvents | inOneshot | inExclUnlink)
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, w := range in.watch {
		if w.gp == gp {
			if mask&inMaskCreate != 0 {
				return sErr(lxEEXIST)
			}
			if mask&inMaskAdd != 0 {
				w.mask |= events
			} else {
				w.mask = events
			}
			return int64(w.wd)
		}
	}
	w := &iwatch{wd: in.next, gp: gp, mask: events}
	in.next++
	in.watch[w.wd] = w
	g.ov.nwatch.Add(1)
	return int64(w.wd)
}

func (g *dguest) inotifyRmWatch(fd int64, wd int32) int64 {
	in, e := g.inotifyOf(fd)
	if e != 0 {
		return sErr(e)
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if _, ok := in.watch[wd]; !ok {
		return sErr(lxEINVAL)
	}
	in.dropLocked(wd)
	return 0
}

// dropLocked removes watch wd and queues its IN_IGNORED; in.mu is held.
func (in *inotify) dropLocked(wd int32) {
	delete(in.watch, wd)
	in.fs.ov.nwatch.Add(-1)
	in.pushLocked(wd, inIgnored, 0, "")
}

func (in *inotify) pushLocked(wd int32, mask, cookie uint32, name string) {
	if in.closed {
		return
	}
	if in.dn != nil {
		in.dnFire = in.dnFire || mask != inIgnored
		return
	}
	n := 0
	if name != "" {
		n = (len(name) + 1 + 15) &^ 15
	}
	rec := make([]byte, 16+n)
	put32(rec[0:], uint32(wd))
	put32(rec[4:], mask)
	put32(rec[8:], cookie)
	put32(rec[12:], uint32(n))
	copy(rec[16:], name)
	if l := len(in.q); l > 0 && string(in.q[l-1]) == string(rec) {
		return // Linux coalesces an event identical to the last queued one
	}
	if len(in.q) >= inQueueMax {
		if len(in.q) == inQueueMax {
			o := make([]byte, 16)
			put32(o[0:], ^uint32(0))
			put32(o[4:], inQOverflow)
			in.q = append(in.q, o)
		}
		return
	}
	in.q = append(in.q, rec)
	in.touch()
	if !in.ready {
		in.w.Write([]byte{1})
		in.ready = true
	}
	in.cond.Broadcast()
}

// deliver raises mask for the object at gp: on a watch of gp itself (no name)
// and, with the child's name, on a watch of its parent directory.
func (in *inotify) deliver(gp string, mask, cookie uint32, self bool) {
	dir, name := path.Dir(gp), path.Base(gp)
	in.mu.Lock()
	in.deliverLocked(gp, dir, name, mask, cookie, self)
	fire := in.dnFire
	in.dnFire = false
	in.mu.Unlock()
	if fire {
		in.dn.send(in.fs)
	}
}

func (in *inotify) deliverLocked(gp, dir, name string, mask, cookie uint32, self bool) {
	for wd, w := range in.watch {
		ev := mask &^ inIsdir
		var nm string
		switch {
		case w.gp == gp && self:
		case w.gp == dir && gp != "/":
			nm = name
		default:
			continue
		}
		if w.mask&ev == 0 {
			continue
		}
		in.pushLocked(wd, mask&(w.mask|inIsdir|inIgnored), cookie, nm)
		if w.mask&inOneshot != 0 {
			in.dropLocked(wd)
		}
	}
}

func (in *inotify) close() {
	in.mu.Lock()
	n := len(in.watch)
	in.watch = map[int32]*iwatch{}
	in.closed = true
	in.cond.Broadcast()
	in.mu.Unlock()
	in.fs.ov.nwatch.Add(int32(-n))
	if in.w != nil {
		in.w.Close()
	}
	in.fs.ov.inotifyMu.Lock()
	ws := in.fs.ov.watchers
	for i, x := range ws {
		if x == in {
			in.fs.ov.watchers = append(ws[:i:i], ws[i+1:]...)
			break
		}
	}
	in.fs.ov.inotifyMu.Unlock()
}

func (g *dguest) watchers() []*inotify {
	if g.ov.nwatch.Load() == 0 {
		return nil
	}
	g.ov.inotifyMu.Lock()
	defer g.ov.inotifyMu.Unlock()
	return append([]*inotify(nil), g.ov.watchers...)
}

// notify raises an event on the object at gp (parent watch with name, own
// watch without).
func (g *dguest) notify(gp string, mask, cookie uint32) {
	for _, in := range g.watchers() {
		in.deliver(gp, mask, cookie, true)
	}
}

func (g *dguest) notifyFd(d *dfile, mask uint32) {
	if g.ov.nwatch.Load() == 0 || d.fx.unnamed || !strings.HasPrefix(d.name(), "/") {
		return
	}
	if d.isDir {
		mask |= inIsdir
	}
	g.notify(d.name(), mask, 0)
}

// notifyRemoved raises IN_DELETE on the parent and IN_DELETE_SELF + IN_IGNORED
// on a watch of the object itself.
func (g *dguest) notifyRemoved(gp string, dir bool) {
	ws := g.watchers()
	if ws == nil {
		return
	}
	var isdir uint32
	if dir {
		isdir = inIsdir
	}
	for _, in := range ws {
		in.deliver(gp, inDelete|isdir, 0, false)
		in.mu.Lock()
		for wd, w := range in.watch {
			if w.gp == gp || strings.HasPrefix(w.gp, gp+"/") {
				if w.gp == gp && w.mask&inDeleteSelf != 0 {
					in.pushLocked(wd, inDeleteSelf, 0, "")
				}
				in.dropLocked(wd)
			}
		}
		in.mu.Unlock()
	}
}

// notifyMoved raises the IN_MOVED_FROM/IN_MOVED_TO pair (one cookie) and
// IN_MOVE_SELF, and moves watches inside the renamed tree to the new path, as
// Linux watches follow the inode.
func (g *dguest) notifyMoved(from, to string, dir bool) {
	ws := g.watchers()
	if ws == nil {
		return
	}
	cookie := g.ov.cookie.Add(1)
	var isdir uint32
	if dir {
		isdir = inIsdir
	}
	for _, in := range ws {
		in.deliver(from, inMovedFrom|isdir, cookie, false)
		in.deliver(to, inMovedTo|isdir, cookie, false)
		in.mu.Lock()
		for wd, w := range in.watch {
			switch {
			case w.gp == from:
				w.gp = to
				if w.mask&inMoveSelf != 0 {
					in.pushLocked(wd, inMoveSelf, 0, "")
				}
			case strings.HasPrefix(w.gp, from+"/"):
				w.gp = to + w.gp[len(from):]
			}
		}
		in.mu.Unlock()
	}
}

// inotifyRead returns whole queued events; a buffer too small for the next one
// is EINVAL. It blocks while the queue is empty unless the fd is non-blocking.
func (g *dguest) inotifyRead(d *dfile, bufAddr uint64, n int64) int64 {
	in := d.fx.in
	in.mu.Lock()
	for len(in.q) == 0 {
		if d.nonblock.Load() || in.closed {
			in.mu.Unlock()
			return sErr(lxEAGAIN)
		}
		done := make(chan struct{})
		go func() {
			select {
			case <-g.ch.done:
			case <-g.ch.stop:
			case <-done:
				return
			}
			in.mu.Lock()
			in.cond.Broadcast()
			in.mu.Unlock()
		}()
		intr := !g.condWaitIntr(in.cond)
		close(done)
		if intr {
			in.mu.Unlock()
			return restartSys
		}
		select {
		case <-g.ch.done:
			in.mu.Unlock()
			return sErr(lxEINTR)
		case <-g.ch.stop:
			in.mu.Unlock()
			return sErr(lxEINTR)
		default:
		}
	}
	var out []byte
	for len(in.q) > 0 && int64(len(out)+len(in.q[0])) <= n {
		out = append(out, in.q[0]...)
		in.q = in.q[1:]
	}
	if len(out) == 0 {
		in.mu.Unlock()
		return sErr(lxEINVAL)
	}
	if len(in.q) == 0 && in.ready {
		var b [1]byte
		readNonblock(d.f, b[:])
		in.ready = false
	}
	in.mu.Unlock()
	if err := g.writeMem(bufAddr, out); err != nil {
		return sErr(lxEFAULT)
	}
	return int64(len(out))
}
