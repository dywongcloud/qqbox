//go:build darwin || linux || windows

package sandbox

// Guest credentials. The guest starts as root with every capability and may
// switch ids the way Linux allows (setuid & co., capabilities following the
// uid transitions as cap_emulate_setxuid does). Threads share their process's
// credentials: musl and glibc already broadcast set*id to every thread.

const (
	lxCapget      = 90
	lxCapset      = 91
	lxPersonality = 92
	lxUnshare     = 97
	lxSetregid    = 143
	lxSetgid      = 144
	lxSetreuid    = 145
	lxSetuid      = 146
	lxSetresuid   = 147
	lxGetresuid   = 148
	lxSetresgid   = 149
	lxGetresgid   = 150
	lxSetfsuid    = 151
	lxSetfsgid    = 152
	lxGetgroups   = 158
	lxSetgroups   = 159
	lxPrctl       = 167
	lxGetuid      = 174
	lxGeteuid     = 175
	lxGetgid      = 176
	lxGetegid     = 177

	capLast      = 40 // CAP_CHECKPOINT_RESTORE
	capAll       = uint64(1)<<(capLast+1) - 1
	capKill      = 5
	capSetgid    = 6
	capSetuid    = 7
	capSetpcap   = 8
	capSysPtrace = 19
	capMknod     = 27
	ngroupsMax   = 65536
	noChange     = ^uint32(0) // -1: leave this id unchanged
	lxCapV1      = 0x19980330
	lxCapV2      = 0x20071026
	lxCapV3      = 0x20080522
	lxPrCapAmb   = 47
	lxPrSetName  = 15
	lxPrGetName  = 16
	lxSeccomp    = 277
)

type creds struct {
	ruid, euid, suid, fsuid uint32
	rgid, egid, sgid, fsgid uint32
	groups                  []uint32

	capEff, capPrm, capInh, capBnd, capAmb uint64
	keepcaps                               bool
	securebits                             uint32
	userns                                 bool // in a new user namespace (unshare CLONE_NEWUSER)
}

func rootCreds() creds {
	return creds{capEff: capAll, capPrm: capAll, capBnd: capAll}
}

// initCreds is the first process's credentials. Nil, or an all-zero user, runs
// as root with full capabilities; a non-root user carries its ids and groups
// and holds no capabilities, as a process started under an image's USER does.
func initCreds(u *UserID) creds {
	if u == nil || (u.Uid == 0 && u.Gid == 0 && len(u.Groups) == 0) {
		return rootCreds()
	}
	c := creds{
		ruid: u.Uid, euid: u.Uid, suid: u.Uid, fsuid: u.Uid,
		rgid: u.Gid, egid: u.Gid, sgid: u.Gid, fsgid: u.Gid,
		groups: append([]uint32(nil), u.Groups...),
		capBnd: capAll,
	}
	if u.Uid == 0 {
		c.capEff, c.capPrm = capAll, capAll
	}
	return c
}

func (c creds) clone() creds {
	c.groups = append([]uint32(nil), c.groups...)
	return c
}

func (c *creds) capable(cap uint) bool { return c.capEff&(1<<cap) != 0 }

// fixCaps applies Linux's capability rules for a uid change from (r, e, s)
// (cap_emulate_setxuid): leaving root for all three ids drops the permitted
// and effective sets unless keepcaps; leaving euid 0 drops the effective set;
// regaining euid 0 restores effective from permitted.
func (c *creds) fixCaps(oldR, oldE, oldS uint32) {
	wasRoot := oldR == 0 || oldE == 0 || oldS == 0
	if wasRoot && c.ruid != 0 && c.euid != 0 && c.suid != 0 && !c.keepcaps {
		c.capPrm, c.capEff, c.capAmb = 0, 0, 0
	}
	if oldE == 0 && c.euid != 0 {
		c.capEff = 0
	}
	if oldE != 0 && c.euid == 0 {
		c.capEff = c.capPrm
	}
}

// onExec applies execve's credential transform for a file without file
// capabilities or set-id bits: the saved ids take the effective ones, and root
// (by real or effective uid) gets the bounding set.
func (c *creds) onExec() {
	c.suid, c.fsuid = c.euid, c.euid
	c.sgid, c.fsgid = c.egid, c.egid
	c.keepcaps = false
	if c.ruid == 0 || c.euid == 0 {
		c.capPrm = c.capBnd | c.capAmb
		if c.euid == 0 {
			c.capEff = c.capPrm
		} else {
			c.capEff = c.capAmb
		}
	} else {
		c.capPrm, c.capEff = c.capAmb, c.capAmb
	}
}

func is(v uint32, set ...uint32) bool {
	for _, x := range set {
		if v == x {
			return true
		}
	}
	return false
}

// withCreds runs f on the caller's credentials under the registry lock. As
// Linux's commit_creds does, a change of effective or fs ids, or a gain in
// permitted capabilities, makes the process non-dumpable (suid_dumpable 0).
func (g *dguest) withCreds(f func(c *creds) int64) int64 {
	g.dfs.mu.Lock()
	defer g.dfs.mu.Unlock()
	old := g.ps.cred
	r := f(&g.ps.cred)
	c := &g.ps.cred
	if c.euid != old.euid || c.egid != old.egid || c.fsuid != old.fsuid || c.fsgid != old.fsgid || c.capPrm&^old.capPrm != 0 {
		g.ps.dumpable = 0
	}
	return r
}

// ptraceMayAccess is Linux's PTRACE_MODE_ATTACH_REALCREDS check of me against
// t: same real ids all round, t dumpable and holding no capability me lacks,
// or else CAP_SYS_PTRACE. The caller holds mu.
func ptraceMayAccess(me, t *pstate) bool {
	if me == t {
		return true
	}
	c, tc := &me.cred, &t.cred
	if c.capable(capSysPtrace) {
		return true
	}
	return is(c.ruid, tc.ruid) && is(c.ruid, tc.euid) && is(c.ruid, tc.suid) &&
		is(c.rgid, tc.rgid) && is(c.rgid, tc.egid) && is(c.rgid, tc.sgid) &&
		t.dumpable == 1 && tc.capPrm&^c.capPrm == 0
}

func (g *dguest) setuid(uid uint32) int64 {
	return g.withCreds(func(c *creds) int64 {
		r, e, s := c.ruid, c.euid, c.suid
		switch {
		case uid == noChange:
			return sErr(lxEINVAL)
		case c.capable(capSetuid):
			c.ruid, c.euid, c.suid, c.fsuid = uid, uid, uid, uid
		case is(uid, c.ruid, c.suid):
			c.euid, c.fsuid = uid, uid
		default:
			return sErr(lxEPERM)
		}
		c.fixCaps(r, e, s)
		return 0
	})
}

func (g *dguest) setreuid(ruid, euid uint32) int64 {
	return g.withCreds(func(c *creds) int64 {
		r, e, s := c.ruid, c.euid, c.suid
		priv := c.capable(capSetuid)
		if ruid != noChange && !priv && !is(ruid, c.ruid, c.euid) ||
			euid != noChange && !priv && !is(euid, c.ruid, c.euid, c.suid) {
			return sErr(lxEPERM)
		}
		if ruid != noChange {
			c.ruid = ruid
		}
		if euid != noChange {
			c.euid = euid
		}
		if ruid != noChange || euid != noChange && euid != r {
			c.suid = c.euid
		}
		c.fsuid = c.euid
		c.fixCaps(r, e, s)
		return 0
	})
}

func (g *dguest) setresuid(ruid, euid, suid uint32) int64 {
	return g.withCreds(func(c *creds) int64 {
		r, e, s := c.ruid, c.euid, c.suid
		if !c.capable(capSetuid) {
			for _, v := range []uint32{ruid, euid, suid} {
				if v != noChange && !is(v, r, e, s) {
					return sErr(lxEPERM)
				}
			}
		}
		if ruid != noChange {
			c.ruid = ruid
		}
		if euid != noChange {
			c.euid = euid
		}
		if suid != noChange {
			c.suid = suid
		}
		c.fsuid = c.euid
		c.fixCaps(r, e, s)
		return 0
	})
}

func (g *dguest) setfsuid(uid uint32) int64 {
	return g.withCreds(func(c *creds) int64 {
		old := c.fsuid
		if uid != noChange && (c.capable(capSetuid) || is(uid, c.ruid, c.euid, c.suid, c.fsuid)) {
			c.fsuid = uid
		}
		return int64(old)
	})
}

func (g *dguest) setgid(gid uint32) int64 {
	return g.withCreds(func(c *creds) int64 {
		switch {
		case gid == noChange:
			return sErr(lxEINVAL)
		case c.capable(capSetgid):
			c.rgid, c.egid, c.sgid, c.fsgid = gid, gid, gid, gid
		case is(gid, c.rgid, c.sgid):
			c.egid, c.fsgid = gid, gid
		default:
			return sErr(lxEPERM)
		}
		return 0
	})
}

func (g *dguest) setregid(rgid, egid uint32) int64 {
	return g.withCreds(func(c *creds) int64 {
		old := c.rgid
		priv := c.capable(capSetgid)
		if rgid != noChange && !priv && !is(rgid, c.rgid, c.egid) ||
			egid != noChange && !priv && !is(egid, c.rgid, c.egid, c.sgid) {
			return sErr(lxEPERM)
		}
		if rgid != noChange {
			c.rgid = rgid
		}
		if egid != noChange {
			c.egid = egid
		}
		if rgid != noChange || egid != noChange && egid != old {
			c.sgid = c.egid
		}
		c.fsgid = c.egid
		return 0
	})
}

func (g *dguest) setresgid(rgid, egid, sgid uint32) int64 {
	return g.withCreds(func(c *creds) int64 {
		if !c.capable(capSetgid) {
			for _, v := range []uint32{rgid, egid, sgid} {
				if v != noChange && !is(v, c.rgid, c.egid, c.sgid) {
					return sErr(lxEPERM)
				}
			}
		}
		if rgid != noChange {
			c.rgid = rgid
		}
		if egid != noChange {
			c.egid = egid
		}
		if sgid != noChange {
			c.sgid = sgid
		}
		c.fsgid = c.egid
		return 0
	})
}

func (g *dguest) setfsgid(gid uint32) int64 {
	return g.withCreds(func(c *creds) int64 {
		old := c.fsgid
		if gid != noChange && (c.capable(capSetgid) || is(gid, c.rgid, c.egid, c.sgid, c.fsgid)) {
			c.fsgid = gid
		}
		return int64(old)
	})
}

// getres writes three u32 ids to three guest pointers.
func (g *dguest) getres(ids [3]uint32, addrs [3]uint64) int64 {
	for i, a := range addrs {
		var b [4]byte
		put32(b[:], ids[i])
		if g.writeMem(a, b[:]) != nil {
			return sErr(lxEFAULT)
		}
	}
	return 0
}

func (g *dguest) getgroups(size int64, list uint64) int64 {
	g.dfs.mu.Lock()
	groups := append([]uint32(nil), g.ps.cred.groups...)
	g.dfs.mu.Unlock()
	if size < 0 {
		return sErr(lxEINVAL)
	}
	if size == 0 {
		return int64(len(groups))
	}
	if size < int64(len(groups)) {
		return sErr(lxEINVAL)
	}
	if len(groups) > 0 {
		b := make([]byte, 4*len(groups))
		for i, v := range groups {
			put32(b[4*i:], v)
		}
		if g.writeMem(list, b) != nil {
			return sErr(lxEFAULT)
		}
	}
	return int64(len(groups))
}

func (g *dguest) setgroups(size int64, list uint64) int64 {
	if size < 0 || size > ngroupsMax {
		return sErr(lxEINVAL)
	}
	g.dfs.mu.Lock()
	priv := g.ps.cred.capable(capSetgid)
	g.dfs.mu.Unlock()
	if !priv {
		return sErr(lxEPERM)
	}
	var groups []uint32
	if size > 0 {
		b, err := g.readMem(list, int(4*size))
		if err != nil {
			return sErr(lxEFAULT)
		}
		groups = make([]uint32, size)
		for i := range groups {
			groups[i] = le32(b[4*i:])
		}
	}
	return g.withCreds(func(c *creds) int64 {
		c.groups = groups
		return 0
	})
}

// capget/capset use the v1 (one 32-bit word set) or v2/v3 (two) layouts of
// struct __user_cap_data_struct {u32 effective, permitted, inheritable}. An
// unknown version gets the preferred one written back and EINVAL, which is how
// libcap probes.
func capWords(version uint32) int {
	switch version {
	case lxCapV1:
		return 1
	case lxCapV2, lxCapV3:
		return 2
	}
	return 0
}

func (g *dguest) capget(hdr, data uint64) int64 {
	h, err := g.readMem(hdr, 8)
	if err != nil {
		return sErr(lxEFAULT)
	}
	n := capWords(le32(h))
	if n == 0 {
		var b [4]byte
		put32(b[:], lxCapV3)
		if g.writeMem(hdr, b[:]) != nil {
			return sErr(lxEFAULT)
		}
		if data == 0 {
			return 0
		}
		return sErr(lxEINVAL)
	}
	pid := int(int32(le32(h[4:])))
	if pid < 0 {
		return sErr(lxEINVAL)
	}
	g.dfs.mu.Lock()
	p, e := g.procOf(pid)
	var c creds
	if e == 0 {
		c = p.cred
	}
	g.dfs.mu.Unlock()
	if e != 0 {
		return sErr(e)
	}
	if data == 0 {
		return 0
	}
	b := make([]byte, 12*n)
	for i := 0; i < n; i++ {
		put32(b[12*i:], uint32(c.capEff>>(32*i)))
		put32(b[12*i+4:], uint32(c.capPrm>>(32*i)))
		put32(b[12*i+8:], uint32(c.capInh>>(32*i)))
	}
	if g.writeMem(data, b) != nil {
		return sErr(lxEFAULT)
	}
	return 0
}

func (g *dguest) capset(hdr, data uint64) int64 {
	h, err := g.readMem(hdr, 8)
	if err != nil {
		return sErr(lxEFAULT)
	}
	n := capWords(le32(h))
	if n == 0 {
		var b [4]byte
		put32(b[:], lxCapV3)
		g.writeMem(hdr, b[:])
		return sErr(lxEINVAL)
	}
	if pid := int(int32(le32(h[4:]))); pid != 0 && pid != g.ps.tgid && pid != g.pid {
		return sErr(lxEPERM)
	}
	b, err := g.readMem(data, 12*n)
	if err != nil {
		return sErr(lxEFAULT)
	}
	var eff, prm, inh uint64
	for i := 0; i < n; i++ {
		eff |= uint64(le32(b[12*i:])) << (32 * i)
		prm |= uint64(le32(b[12*i+4:])) << (32 * i)
		inh |= uint64(le32(b[12*i+8:])) << (32 * i)
	}
	return g.withCreds(func(c *creds) int64 {
		if n == 1 {
			// v1 sets only the low word; the upper capabilities are kept.
			eff |= c.capEff &^ 0xffffffff
			prm |= c.capPrm &^ 0xffffffff
			inh |= c.capInh &^ 0xffffffff
		}
		eff, prm, inh = eff&capAll, prm&capAll, inh&capAll
		if !c.capable(capSetpcap) && inh&^(c.capInh|c.capPrm) != 0 ||
			inh&^(c.capInh|c.capBnd) != 0 ||
			prm&^c.capPrm != 0 ||
			eff&^prm != 0 {
			return sErr(lxEPERM)
		}
		c.capEff, c.capPrm, c.capInh = eff, prm, inh
		c.capAmb &= prm & inh
		return 0
	})
}

// unshare enters new namespaces. The sandbox is the supervisor, so the
// isolation ones a program's own sandbox asks for (pid, net, mount, ipc, uts,
// cgroup) are accepted as given, and CLONE_NEWUSER makes the caller root in a
// new user namespace with every capability, so a Chromium-style unprivileged
// sandbox proceeds; the uid/gid maps it then writes complete the mapping.
func (g *dguest) unshare(flags int64) int64 {
	const (
		cloneNewUser = 0x10000000
		cloneFiles   = 0x400
		cloneFs      = 0x200
		cloneSysvsem = 0x40000
	)
	if flags&^(lxCloneNewMask|cloneFiles|cloneFs|cloneSysvsem) != 0 {
		return sErr(lxEINVAL)
	}
	const cloneNewPid = 0x20000000
	const cloneNewNet = 0x40000000
	if flags&cloneNewUser != 0 {
		g.withCreds(func(c *creds) int64 {
			c.userns = true
			c.capEff, c.capPrm, c.capBnd, c.capInh = capAll, capAll, capAll, capAll
			return 0
		})
	}
	if flags&cloneNewPid != 0 {
		// unshare(CLONE_NEWPID) does not move the caller; its next children
		// become pid 1 (then 2, ...) in a new pid namespace.
		fs := g.dfs
		fs.mu.Lock()
		g.ps.childPidNs = g.ps.ns.child(fs.nextNsID)
		fs.nextNsID++
		fs.mu.Unlock()
	}
	if flags&cloneNewNet != 0 {
		// unshare(CLONE_NEWNET) moves the caller into a new empty network
		// namespace, cutting it off from the host network.
		fs := g.dfs
		fs.mu.Lock()
		g.ps.netns.Store(int32(fs.nextNsID))
		fs.nextNsID++
		fs.mu.Unlock()
	}
	const cloneNewUts = 0x04000000
	if flags&cloneNewUts != 0 {
		// unshare(CLONE_NEWUTS) gives the caller a private copy of the node and
		// domain names.
		g.dfs.mu.Lock()
		g.ps.uts = g.utsNS().clone()
		g.dfs.mu.Unlock()
	}
	const cloneNewNs = 0x00020000
	if flags&cloneNewNs != 0 {
		// unshare(CLONE_NEWNS) moves the caller into a new mount namespace; the
		// mounts it then makes (a container runtime's relocated /proc and tmpfs
		// mounts) are tagged with it and dropped when its last process exits.
		fs := g.dfs
		fs.mu.Lock()
		g.ps.mntns = fs.nextNsID
		fs.nextNsID++
		fs.mu.Unlock()
	}
	if flags&cloneFs != 0 && g.pfs != nil {
		// unshare(CLONE_FS) detaches this thread's root, cwd and umask from the
		// threads it shared them with, so a later chroot or chdir is private to
		// it. docker's chrootarchive relies on this: it locks a thread, unshares
		// CLONE_FS, chroots into the layer dir to untar it, and expects the
		// daemon's other threads to keep their root -- without it the chroot
		// leaks process-wide and the next absolute-path write (the layer's diff
		// metadata) resolves inside the layer dir and fails ENOENT.
		g.pfs = g.pfs.clone()
	}
	return 0
}

// addrNoRandomize is personality(2)'s ADDR_NO_RANDOMIZE: the next execve
// places the program at its unrandomized bases.
const (
	addrNoRandomize  = 0x0040000
	addrCompatLayout = 0x0200000
)

func (g *dguest) personality(p uint32) int64 {
	g.dfs.mu.Lock()
	defer g.dfs.mu.Unlock()
	old := g.ps.personality
	if p != 0xffffffff {
		g.ps.personality = p
	}
	return int64(old)
}

// prctl serves the process-attribute options programs use; anything else is
// EINVAL, as for an option the kernel does not know.
func (g *dguest) prctl(a [6]uint64) int64 {
	const (
		prSetPdeathsig      = 1
		prGetPdeathsig      = 2
		prGetDumpable       = 3
		prSetDumpable       = 4
		prGetKeepcaps       = 7
		prSetKeepcaps       = 8
		prGetTiming         = 13
		prSetTiming         = 14
		prGetSeccomp        = 21
		prSetSeccomp        = 22
		prCapbsetRead       = 23
		prCapbsetDrop       = 24
		prGetSecurebits     = 27
		prSetSecurebits     = 28
		prSetTimerslack     = 29
		prGetTimerslack     = 30
		prTaskPerfDisable   = 31
		prTaskPerfEnable    = 32
		prMceKill           = 33
		prMceKillGet        = 34
		prSetChildSubreaper = 36
		prGetChildSubreaper = 37
		prSetNoNewPrivs     = 38
		prGetNoNewPrivs     = 39
		prSetThpDisable     = 41
		prGetThpDisable     = 42
		prGetSpecCtrl       = 52
		prSetSpecCtrl       = 53
		prSetPtracer        = 0x59616d61
	)
	opt, arg2 := uint32(a[0]), a[1]
	fs := g.dfs
	putInt := func(addr uint64, v int32) int64 {
		var b [4]byte
		put32(b[:], uint32(v))
		if g.writeMem(addr, b[:]) != nil {
			return sErr(lxEFAULT)
		}
		return 0
	}
	switch opt {
	case lxPrSetName:
		b, err := g.readCStr(arg2)
		if err != nil {
			return sErr(lxEFAULT)
		}
		if len(b) > 15 {
			b = b[:15]
		}
		fs.mu.Lock()
		g.ps.comm[g.pid] = b
		fs.mu.Unlock()
		return 0
	case lxPrGetName:
		fs.mu.Lock()
		name := g.ps.comm[g.pid]
		fs.mu.Unlock()
		var b [16]byte
		copy(b[:15], name)
		if g.writeMem(arg2, b[:]) != nil {
			return sErr(lxEFAULT)
		}
		return 0
	case prSetPdeathsig:
		if arg2 > 64 {
			return sErr(lxEINVAL)
		}
		fs.mu.Lock()
		g.ps.pdeathsig = int(arg2)
		fs.mu.Unlock()
		return 0
	case prGetPdeathsig:
		fs.mu.Lock()
		v := g.ps.pdeathsig
		fs.mu.Unlock()
		return putInt(arg2, int32(v))
	case prGetDumpable:
		fs.mu.Lock()
		defer fs.mu.Unlock()
		return int64(g.ps.dumpable)
	case prSetDumpable:
		if arg2 > 1 {
			return sErr(lxEINVAL)
		}
		fs.mu.Lock()
		g.ps.dumpable = int(arg2)
		fs.mu.Unlock()
		return 0
	case prGetKeepcaps:
		return g.withCreds(func(c *creds) int64 { return b2i(c.keepcaps) })
	case prSetKeepcaps:
		if arg2 > 1 {
			return sErr(lxEINVAL)
		}
		return g.withCreds(func(c *creds) int64 { c.keepcaps = arg2 == 1; return 0 })
	case prGetTiming:
		return 0 // PR_TIMING_STATISTICAL
	case prSetTiming:
		if arg2 != 0 {
			return sErr(lxEINVAL)
		}
		return 0
	case prGetSeccomp:
		return int64(g.ps.seccomp.Load())
	case prSetSeccomp:
		// SECCOMP_MODE_STRICT(1) or SECCOMP_MODE_FILTER(2). The guest's own BPF
		// filter is defence in depth that the supervisor's syscall mediation
		// already subsumes, so the filter is accepted but not run in the guest.
		return g.setSeccomp(arg2, a[2])
	case prCapbsetRead:
		if arg2 > capLast {
			return sErr(lxEINVAL)
		}
		return g.withCreds(func(c *creds) int64 { return b2i(c.capBnd&(1<<arg2) != 0) })
	case prCapbsetDrop:
		if arg2 > capLast {
			return sErr(lxEINVAL)
		}
		return g.withCreds(func(c *creds) int64 {
			if !c.capable(capSetpcap) {
				return sErr(lxEPERM)
			}
			c.capBnd &^= 1 << arg2
			return 0
		})
	case prGetSecurebits:
		return g.withCreds(func(c *creds) int64 { return int64(c.securebits) })
	case prSetSecurebits:
		return g.withCreds(func(c *creds) int64 {
			if !c.capable(capSetpcap) {
				return sErr(lxEPERM)
			}
			c.securebits = uint32(arg2)
			return 0
		})
	case prGetTimerslack:
		return 50000
	case prSetTimerslack, prTaskPerfDisable, prTaskPerfEnable, prMceKill, prSetThpDisable, prSetSpecCtrl, prSetPtracer:
		return 0
	case prMceKillGet, prGetThpDisable, prGetSpecCtrl:
		return 0
	case prSetChildSubreaper:
		fs.mu.Lock()
		g.ps.subreaper = arg2 != 0
		fs.mu.Unlock()
		return 0
	case prGetChildSubreaper:
		fs.mu.Lock()
		v := g.ps.subreaper
		fs.mu.Unlock()
		return putInt(arg2, int32(b2i(v)))
	case prSetNoNewPrivs:
		if arg2 != 1 || a[2]|a[3]|a[4] != 0 {
			return sErr(lxEINVAL)
		}
		fs.mu.Lock()
		g.ps.noNewPrivs = true
		fs.mu.Unlock()
		return 0
	case prGetNoNewPrivs:
		if arg2|a[2]|a[3]|a[4] != 0 {
			return sErr(lxEINVAL)
		}
		fs.mu.Lock()
		defer fs.mu.Unlock()
		return b2i(g.ps.noNewPrivs)
	case lxPrCapAmb:
		return g.capAmbient(arg2, a[2], a[3]|a[4])
	}
	return sErr(lxEINVAL)
}

func (g *dguest) capAmbient(op, cap, rest uint64) int64 {
	const (
		isSet    = 1
		raise    = 2
		lower    = 3
		clearAll = 4
	)
	if rest != 0 {
		return sErr(lxEINVAL)
	}
	return g.withCreds(func(c *creds) int64 {
		if op == clearAll {
			if cap != 0 {
				return sErr(lxEINVAL)
			}
			c.capAmb = 0
			return 0
		}
		if cap > capLast {
			return sErr(lxEINVAL)
		}
		bit := uint64(1) << cap
		switch op {
		case isSet:
			return b2i(c.capAmb&bit != 0)
		case raise:
			if c.capPrm&bit == 0 || c.capInh&bit == 0 {
				return sErr(lxEPERM)
			}
			c.capAmb |= bit
		case lower:
			c.capAmb &^= bit
		default:
			return sErr(lxEINVAL)
		}
		return 0
	})
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func init() {
	cred := func(f func(c *creds) uint32) func(g *dguest, a [6]uint64) int64 {
		return func(g *dguest, a [6]uint64) int64 {
			return g.withCreds(func(c *creds) int64 { return int64(f(c)) })
		}
	}
	syscalls[lxGetuid] = cred(func(c *creds) uint32 { return c.ruid })
	syscalls[lxGeteuid] = cred(func(c *creds) uint32 { return c.euid })
	syscalls[lxGetgid] = cred(func(c *creds) uint32 { return c.rgid })
	syscalls[lxGetegid] = cred(func(c *creds) uint32 { return c.egid })
	syscalls[lxSetuid] = func(g *dguest, a [6]uint64) int64 { return g.setuid(uint32(a[0])) }
	syscalls[lxSetgid] = func(g *dguest, a [6]uint64) int64 { return g.setgid(uint32(a[0])) }
	syscalls[lxSetreuid] = func(g *dguest, a [6]uint64) int64 { return g.setreuid(uint32(a[0]), uint32(a[1])) }
	syscalls[lxSetregid] = func(g *dguest, a [6]uint64) int64 { return g.setregid(uint32(a[0]), uint32(a[1])) }
	syscalls[lxSetresuid] = func(g *dguest, a [6]uint64) int64 {
		return g.setresuid(uint32(a[0]), uint32(a[1]), uint32(a[2]))
	}
	syscalls[lxSetresgid] = func(g *dguest, a [6]uint64) int64 {
		return g.setresgid(uint32(a[0]), uint32(a[1]), uint32(a[2]))
	}
	syscalls[lxGetresuid] = func(g *dguest, a [6]uint64) int64 {
		g.dfs.mu.Lock()
		c := g.ps.cred
		g.dfs.mu.Unlock()
		return g.getres([3]uint32{c.ruid, c.euid, c.suid}, [3]uint64{a[0], a[1], a[2]})
	}
	syscalls[lxGetresgid] = func(g *dguest, a [6]uint64) int64 {
		g.dfs.mu.Lock()
		c := g.ps.cred
		g.dfs.mu.Unlock()
		return g.getres([3]uint32{c.rgid, c.egid, c.sgid}, [3]uint64{a[0], a[1], a[2]})
	}
	syscalls[lxSetfsuid] = func(g *dguest, a [6]uint64) int64 { return g.setfsuid(uint32(a[0])) }
	syscalls[lxSetfsgid] = func(g *dguest, a [6]uint64) int64 { return g.setfsgid(uint32(a[0])) }
	syscalls[lxGetgroups] = func(g *dguest, a [6]uint64) int64 { return g.getgroups(int64(int32(a[0])), a[1]) }
	syscalls[lxSetgroups] = func(g *dguest, a [6]uint64) int64 { return g.setgroups(int64(int32(a[0])), a[1]) }
	syscalls[lxCapget] = func(g *dguest, a [6]uint64) int64 { return g.capget(a[0], a[1]) }
	syscalls[lxCapset] = func(g *dguest, a [6]uint64) int64 { return g.capset(a[0], a[1]) }
	syscalls[lxUnshare] = func(g *dguest, a [6]uint64) int64 { return g.unshare(int64(a[0])) }
	syscalls[lxPersonality] = func(g *dguest, a [6]uint64) int64 { return g.personality(uint32(a[0])) }
	syscalls[lxPrctl] = func(g *dguest, a [6]uint64) int64 { return g.prctl(a) }
	syscalls[lxSeccomp] = func(g *dguest, a [6]uint64) int64 { return g.seccomp(a[0], a[1], a[2]) }
}

// setSeccomp records the seccomp mode a PR_SET_SECCOMP installs. The kernel
// copies the filter from user space before the privilege check, so a filter
// mode with an unreadable pointer is EFAULT: Chromium probes support exactly
// this way (a null filter expecting EFAULT), so this must not short-circuit. A
// real filter needs CAP_SYS_ADMIN or no_new_privs and is then accepted but not
// run in the guest, since the supervisor already mediates every guest syscall.
func (g *dguest) setSeccomp(mode, filter uint64) int64 {
	const modeStrict, modeFilter = 1, 2
	var prog []byte
	switch mode {
	case modeStrict:
		if filter != 0 {
			return sErr(lxEFAULT)
		}
	case modeFilter:
		// The kernel copies the program in before the privilege check, so a bad
		// pointer is EFAULT — which is how Chromium probes for support.
		p, e := g.readFilter(filter)
		if e != 0 {
			return sErr(e)
		}
		// The kernel validates the filter's instructions before the privilege
		// check and rejects an unmodelled or out-of-range one with EINVAL, so a
		// filter never silently fails to enforce (seccomp_check_filter).
		if !bpfValidate(p) {
			return sErr(lxEINVAL)
		}
		prog = p
	default:
		return sErr(lxEINVAL)
	}
	fs := g.dfs
	// A newly installed filter changes whether the stub must route rt_sigreturn
	// past the supervisor; refresh the slots' stdio words once the lock is clear.
	installed := false
	defer func() {
		if installed {
			syncStdio()
		}
	}()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if mode == modeFilter && !g.ps.noNewPrivs && !g.ps.cred.capable(capSysAdmin) {
		return sErr(lxEACCES)
	}
	g.ps.seccomp.Store(int32(mode))
	if prog != nil {
		g.ps.seccompProg = append(g.ps.seccompProg[:len(g.ps.seccompProg):len(g.ps.seccompProg)], prog)
		installed = true
	}
	return 0
}

// seccomp is the seccomp(2) syscall. SET_MODE_STRICT and SET_MODE_FILTER map to
// the PR_SET_SECCOMP modes; GET_ACTION_AVAIL just probes support. Filter flags
// other than TSYNC/LOG/SPEC_ALLOW (and NEW_LISTENER, which qqbox does not
// provide) are rejected.
func (g *dguest) seccomp(op, flags, args uint64) int64 {
	const (
		setModeStrict  = 0
		setModeFilter  = 1
		getActionAvail = 2
		flagTsync      = 1
		flagLog        = 2
		flagSpecAllow  = 4
	)
	switch op {
	case setModeStrict:
		if flags != 0 || args != 0 {
			return sErr(lxEINVAL)
		}
		return g.setSeccomp(1, 0)
	case setModeFilter:
		if flags&^(flagTsync|flagLog|flagSpecAllow) != 0 {
			return sErr(lxEINVAL)
		}
		return g.setSeccomp(2, args)
	case getActionAvail:
		return 0
	}
	return sErr(lxEINVAL)
}
