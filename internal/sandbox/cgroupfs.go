//go:build darwin || linux || windows

package sandbox

import (
	"fmt"
	"path"
	"sort"
	"sync"
)

// cgStore is the sandbox's writable cgroup v2 hierarchy under /sys/fs/cgroup. A
// container runtime (crun under podman) creates the container's cgroup with
// mkdir and configures it through its control files. laboon enforces no real
// limits, so the hierarchy is virtual: mkdir/rmdir add and remove cgroups, a
// control-file write is recorded and read back, and nothing is throttled. It is
// one hierarchy shared by every process of the sandbox — a cgroup one process
// creates is seen by the others — so it is guarded for concurrent syscalls and
// lives in the shared dfs rather than a per-guest namespace.
type cgStore struct {
	mu   sync.Mutex
	dirs map[string]*cgGroup // by guest path; cgRoot is the hierarchy root
}

type cgGroup struct {
	subdirs map[string]bool   // child cgroup names
	files   map[string]string // control-file values the guest wrote
}

const cgRoot = "/sys/fs/cgroup"

func newCGStore() *cgStore {
	return &cgStore{dirs: map[string]*cgGroup{
		cgRoot: {subdirs: map[string]bool{}, files: map[string]string{}},
	}}
}

// change creates (mkdir) or removes (rmdir) the cgroup at gp, a proper path
// under cgRoot.
func (s *cgStore) change(gp string, create bool) lxErrno {
	parent, name := path.Dir(gp), path.Base(gp)
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.dirs[parent]
	if p == nil {
		return lxENOENT // the parent cgroup does not exist
	}
	if create {
		if p.subdirs[name] {
			return lxEEXIST
		}
		p.subdirs[name] = true
		s.dirs[gp] = &cgGroup{subdirs: map[string]bool{}, files: map[string]string{}}
		return 0
	}
	g := s.dirs[gp]
	if g == nil || !p.subdirs[name] {
		return lxENOENT
	}
	if len(g.subdirs) != 0 {
		return lxENOTEMPTY
	}
	delete(p.subdirs, name)
	delete(s.dirs, gp)
	return 0
}

// cgroupDir is the pnode the /sys/fs/cgroup mount and pseudoOpen resolve
// through; both walk it with child/kids, which consult the shared store so a
// created cgroup and its control files appear to every process.
func cgroupDir(s *cgStore) *pnode { return s.dirNode(cgRoot) }

func (s *cgStore) dirNode(gp string) *pnode {
	return &pnode{
		kind: pDir, perm: 0o555,
		look: func(name string) *pnode { return s.child(gp, name) },
		kids: func() []pchild { return s.children(gp) },
	}
}

func (s *cgStore) child(gp, name string) *pnode {
	s.mu.Lock()
	g := s.dirs[gp]
	sub := g != nil && g.subdirs[name]
	s.mu.Unlock()
	if sub {
		return s.dirNode(path.Join(gp, name))
	}
	if _, ok := cgFiles[name]; ok {
		return s.fileNode(gp, name)
	}
	return nil
}

func (s *cgStore) children(gp string) []pchild {
	out := make([]pchild, 0, len(cgFileNames))
	for _, name := range cgFileNames {
		out = append(out, pchild{name, s.fileNode(gp, name)})
	}
	s.mu.Lock()
	var subs []string
	if g := s.dirs[gp]; g != nil {
		for name := range g.subdirs {
			subs = append(subs, name)
		}
	}
	s.mu.Unlock()
	sort.Strings(subs)
	for _, name := range subs {
		out = append(out, pchild{name, s.dirNode(path.Join(gp, name))})
	}
	return out
}

func (s *cgStore) fileNode(gp, name string) *pnode {
	spec := cgFiles[name]
	perm := uint32(0o444)
	if spec.write {
		perm = 0o644
	}
	n := &pnode{kind: pFile, perm: perm, write: spec.write}
	n.gen = func(*dguest) []byte {
		if spec.gen != nil {
			return []byte(spec.gen())
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if g := s.dirs[gp]; g != nil {
			if v, ok := g.files[name]; ok {
				return []byte(v)
			}
		}
		return []byte(spec.def)
	}
	if spec.write {
		n.put = func(_ *dguest, b []byte) lxErrno {
			s.mu.Lock()
			if g := s.dirs[gp]; g != nil {
				g.files[name] = string(b)
			}
			s.mu.Unlock()
			return 0
		}
	}
	return n
}

// cgFile is a cgroup v2 control file: its default content, whether the guest
// may write it (a written value is recorded and read back), and a generator
// for host-derived values.
type cgFile struct {
	def   string
	write bool
	gen   func() string
}

var cgFiles = map[string]cgFile{
	"cgroup.controllers":     {def: "cpuset cpu io memory pids\n"},
	"cgroup.subtree_control": {write: true},
	"cgroup.type":            {def: "domain\n"},
	"cgroup.procs":           {write: true},
	"cgroup.threads":         {write: true},
	"cgroup.events":          {def: "populated 0\nfrozen 0\n"},
	"cgroup.stat":            {def: "nr_descendants 0\nnr_dying_descendants 0\n"},
	"cgroup.freeze":          {def: "0\n", write: true},
	"cgroup.max.depth":       {def: "max\n", write: true},
	"cgroup.max.descendants": {def: "max\n", write: true},
	"cpu.max":                {def: "max 100000\n", write: true},
	"cpu.max.burst":          {def: "0\n", write: true},
	"cpu.weight":             {def: "100\n", write: true},
	"cpu.idle":               {def: "0\n", write: true},
	"cpu.stat":               {def: "usage_usec 0\nuser_usec 0\nsystem_usec 0\nnr_periods 0\nnr_throttled 0\nthrottled_usec 0\n"},
	"io.max":                 {write: true},
	"io.weight":              {def: "default 100\n", write: true},
	"io.stat":                {},
	"memory.max":             {def: "max\n", write: true},
	"memory.high":            {def: "max\n", write: true},
	"memory.low":             {def: "0\n", write: true},
	"memory.min":             {def: "0\n", write: true},
	"memory.swap.max":        {def: "max\n", write: true},
	"memory.swap.current":    {def: "0\n"},
	"memory.current":         {gen: func() string { return fmt.Sprintf("%d\n", memStats().anon) }},
	"memory.events":          {def: "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n"},
	"memory.stat":            {def: "anon 0\nfile 0\nkernel_stack 0\nslab 0\nsock 0\n"},
	"pids.max":               {def: "max\n", write: true},
	"pids.current":           {def: "0\n"},
	"pids.events":            {def: "max 0\n"},
	"cpuset.cpus":            {write: true},
	"cpuset.mems":            {write: true},
	"cpuset.cpus.effective":  {gen: func() string { return cpuList(hostCPUInfo().n) + "\n" }},
	"cpuset.mems.effective":  {def: "0\n"},
}

var cgFileNames = func() []string {
	names := make([]string, 0, len(cgFiles))
	for name := range cgFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}()
