//go:build darwin || linux || windows

package sandbox

// PID namespaces. Every process belongs to a pid namespace; the first one
// (initNs, id 0) is the host view, where a process's namespace pid is just its
// global pid, so nothing translates there and the common single-namespace case
// is unchanged. clone(CLONE_NEWPID) puts the child in a fresh namespace where
// it is pid 1 and its descendants are numbered from 2; a process is seen with a
// different pid in each ancestor namespace (and is invisible from a sibling or
// child namespace). The global pid stays the registry key throughout; only the
// numbers the guest sees are translated, at getpid/gettid/getppid, wait, kill,
// pidfd, /proc and the credentials passed over a unix socket.
//
// All of this is read and written under dfs.mu, the lock that already guards
// the process table.

type pidns struct {
	id       int
	parent   *pidns
	next     int         // next namespace-local pid to hand out
	toLocal  map[int]int // global pid -> this namespace's pid
	toGlobal map[int]int // this namespace's pid -> global pid
}

// newInitNs is the host pid namespace: identity, so it needs no maps.
func newInitNs() *pidns { return &pidns{id: 0} }

// child makes a new namespace nested in ns, its pids starting at 1.
func (ns *pidns) child(id int) *pidns {
	return &pidns{id: id, parent: ns, next: 1, toLocal: map[int]int{}, toGlobal: map[int]int{}}
}

// localOf is the namespace pid of a global pid, 0 if it is not a member.
func (ns *pidns) localOf(global int) int {
	if ns.id == 0 {
		return global
	}
	return ns.toLocal[global]
}

// globalOf is the global pid of a namespace pid, 0 if there is none.
func (ns *pidns) globalOf(local int) int {
	if ns.id == 0 {
		return local
	}
	return ns.toGlobal[local]
}

// register adds a new global pid to ns and to every ancestor up to (not
// including) the host namespace, so the process has a pid in each namespace it
// is visible from. The host namespace is identity and needs no entry. The
// caller holds dfs.mu.
func (ns *pidns) register(global int) {
	for n := ns; n != nil && n.id != 0; n = n.parent {
		n.add(global)
	}
}

// deregister forgets a global pid in ns and every ancestor, the inverse of
// register, once the process or thread is gone for good. The caller holds
// dfs.mu.
func (ns *pidns) deregister(global int) {
	for n := ns; n != nil && n.id != 0; n = n.parent {
		n.drop(global)
	}
}

// add registers a global pid as a new member and returns its namespace pid.
func (ns *pidns) add(global int) int {
	if ns.id == 0 {
		return global
	}
	local := ns.next
	ns.next++
	ns.toLocal[global] = local
	ns.toGlobal[local] = global
	return local
}

// drop forgets a global pid once it is reaped, so its namespace pid is free of
// a stale mapping.
func (ns *pidns) drop(global int) {
	if ns.id == 0 {
		return
	}
	if l, ok := ns.toLocal[global]; ok {
		delete(ns.toGlobal, l)
		delete(ns.toLocal, global)
	}
}

// ancestorOf reports whether ns is at or above other in the namespace tree, so
// a process in other is visible from ns.
func (ns *pidns) ancestorOf(other *pidns) bool {
	for n := other; n != nil; n = n.parent {
		if n == ns {
			return true
		}
	}
	return false
}

// seen is ps's pid as observed from namespace ns: ps's own namespace pid when
// ns is ps's namespace, its pid in that ancestor when ns is one, else 0. The
// caller holds dfs.mu.
func (ns *pidns) seen(ps *pstate) int {
	for n := ps.ns; n != nil; n = n.parent {
		if n == ns {
			return n.localOf(ps.tgid)
		}
	}
	return 0
}

// nsPid is a global pid's value as seen from g's own namespace. In the host
// namespace it is the pid unchanged, with no lock taken; otherwise it reads the
// namespace map under dfs.mu.
func (g *dguest) nsPid(globalPid int) int {
	if g.ps.ns.id == 0 {
		return globalPid
	}
	g.dfs.mu.Lock()
	defer g.dfs.mu.Unlock()
	return g.nsPidLocked(globalPid)
}

// nsPidLocked is pid's value as seen from g's namespace. The caller holds
// dfs.mu.
func (g *dguest) nsPidLocked(globalPid int) int {
	ns := g.ps.ns
	if ps := g.dfs.pt[globalPid]; ps != nil {
		return ns.seen(ps)
	}
	return ns.localOf(globalPid) // a bare thread id, not a thread-group leader
}

// toGlobalPid resolves a pid the guest named, in g's namespace, to a global
// pid; 0 if g's namespace has no such pid. The caller holds dfs.mu.
func (g *dguest) toGlobalPid(nsPid int) int {
	return g.ps.ns.globalOf(nsPid)
}

// nsToGlobal maps a positive pid or tid the guest named to the global value the
// process table keys on; ok is false when the caller's namespace has no such
// id. It takes dfs.mu, so the caller must not hold it.
func (g *dguest) nsToGlobal(nsPid int) (int, bool) {
	if g.ps.ns.id == 0 {
		return nsPid, true
	}
	g.dfs.mu.Lock()
	defer g.dfs.mu.Unlock()
	gp := g.ps.ns.globalOf(nsPid)
	return gp, gp != 0
}
