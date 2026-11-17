package sandbox

import "slices"

// Linux discretionary access control (generic_permission and the capability
// overrides of capable_wrt_inode_uidgid), for backends that serve the file
// system themselves instead of leaving it to a kernel.

const (
	mayExec  = 1
	mayWrite = 2
	mayRead  = 4

	capChown         = 0
	capDacOverride   = 1
	capDacReadSearch = 2
	capFowner        = 3
)

// dacCred is the part of a process's credentials file access looks at: the fs
// ids, the supplementary groups and the effective capabilities.
type dacCred struct {
	uid, gid uint32
	groups   []uint32
	caps     uint64
}

func (c *dacCred) has(cp uint) bool { return c.caps&(1<<cp) != 0 }

func (c *dacCred) inGroup(gid uint32) bool { return c.gid == gid || slices.Contains(c.groups, gid) }

// dacPermits reports whether c may access an inode with the given owner, group
// and mode (permission bits) for want, a mask of mayRead, mayWrite and mayExec.
func dacPermits(c *dacCred, owner, group, mode uint32, isDir bool, want int) bool {
	bits := mode
	switch {
	case c.uid == owner:
		bits = mode >> 6
	case c.inGroup(group):
		bits = mode >> 3
	}
	if uint32(want)&^(bits&7) == 0 {
		return true
	}
	if isDir {
		return c.has(capDacOverride) || want&mayWrite == 0 && c.has(capDacReadSearch)
	}
	if c.has(capDacOverride) && (want&mayExec == 0 || mode&0o111 != 0) {
		return true
	}
	return want == mayRead && c.has(capDacReadSearch)
}

// dacStickyOK is the sticky-directory rule for unlink and rename: in a +t
// directory only the file's owner, the directory's owner or CAP_FOWNER may
// remove an entry.
func dacStickyOK(c *dacCred, dirOwner, dirMode, fileOwner uint32) bool {
	return dirMode&0o1000 == 0 || c.uid == fileOwner || c.uid == dirOwner || c.has(capFowner)
}
