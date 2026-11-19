//go:build darwin || linux || windows

package sandbox

import (
	"encoding/binary"
	"os"
)

const (
	lxBpf = 280 // generic/arm64 bpf() number

	bpfProgLoad   = 5
	bpfProgAttach = 8
	bpfProgDetach = 9
	bpfProgQuery  = 16

	bpfProgTypeCgroupDevice = 15 // BPF_PROG_TYPE_CGROUP_DEVICE: prog_type is attr[0]
)

func init() {
	syscalls[lxBpf] = func(g *dguest, a [6]uint64) int64 { return g.bpf(a[0], a[1], a[2]) }
}

// bpf models the one eBPF use qqbox can honor: the cgroup v2 device controller.
// A container runtime (crun) loads a BPF_PROG_TYPE_CGROUP_DEVICE program and
// attaches it to the container's cgroup to allow/deny device access; qqbox does
// not run eBPF but already mediates device access through the synthetic /dev set,
// so for that one program type the filter is a no-op (PROG_LOAD hands back a
// usable descriptor so attach and the later close succeed, attach/detach report
// success, a query reports nothing attached). Any OTHER program type is NOT
// modeled, so PROG_LOAD rejects it with EINVAL rather than hand back a filter
// that silently does nothing -- a guest that loads a socket filter, LSM or XDP
// program must see it fail, not believe it is enforcing.
func (g *dguest) bpf(cmd, attrAddr, size uint64) int64 {
	switch cmd {
	case bpfProgLoad:
		// prog_type is the first u32 of the BPF_PROG_LOAD attr.
		pt, err := g.readMem(attrAddr, 4)
		if err != nil {
			return sErr(lxEFAULT)
		}
		if le32(pt) != bpfProgTypeCgroupDevice {
			return sErr(lxEINVAL)
		}
		f, err := os.Open(os.DevNull)
		if err != nil {
			return sErr(osErrno(err))
		}
		d := &dfile{f: f, gp: "anon_inode:bpf-prog", stub: -1}
		fd := g.ft.add(d, 0)
		g.ft.setCloexec(fd, true) // the kernel creates bpf fds close-on-exec
		return int64(fd)
	case bpfProgAttach, bpfProgDetach:
		return 0
	case bpfProgQuery:
		// Report zero attached programs. prog_cnt is a u32 at offset 24 of the
		// query attr (after target_fd, attach_type, query_flags, attach_flags
		// and the prog_ids pointer); the caller reads it to size its results.
		if size >= 28 {
			var z [4]byte
			binary.LittleEndian.PutUint32(z[:], 0)
			if err := g.writeMem(attrAddr+24, z[:]); err != nil {
				return sErr(lxEFAULT)
			}
		}
		return 0
	}
	return sErr(lxENOSYS)
}
