//go:build darwin || linux || windows

package sandbox

import "sync"

// stdioSlots is every live guest thread, whose slot stdio word follows its fd
// table: which of fds 0-2 the stub may serve itself (stdioMask). It is global
// because an open-file description (and so its O_NONBLOCK flag) is shared
// across the processes a fork creates.
var stdioSlots = struct {
	sync.Mutex
	m map[*dguest]struct{}
}{m: map[*dguest]struct{}{}}

func (g *dguest) attachStdio() {
	stdioSlots.Lock()
	stdioSlots.m[g] = struct{}{}
	stdioSlots.Unlock()
	syncStdio()
}

func (g *dguest) detachStdio() {
	stdioSlots.Lock()
	delete(stdioSlots.m, g)
	g.storeStdio(0)
	stdioSlots.Unlock()
}

// syncStdio recomputes each slot's stdio word. It runs after anything that can
// change what fds 0-2 refer to or their blocking mode (close, dup onto them,
// O_NONBLOCK, exec); a stale bit would send IO to the wrong file, a missing
// one only costs a round trip.
func syncStdio() {
	stdioSlots.Lock()
	defer stdioSlots.Unlock()
	for g := range stdioSlots.m {
		g.storeStdio(g.stdioMask())
	}
}
