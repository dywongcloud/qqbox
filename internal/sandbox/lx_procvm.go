//go:build darwin || linux || windows

package sandbox

// process_vm_readv / process_vm_writev: a flat byte-stream copy between the
// caller's local_iov and another process's remote_iov. A guest that is itself a
// laboon supervisor uses these to read and write its own stub's memory, so
// nesting needs them served across guest processes.
//
// The caller's own (local) memory is always reachable through its channel; the
// target's (remote) memory is reached per host: directly on Linux (the target
// is a host process, read with process_vm) and Windows (its process handle),
// and through the caller's channel on macOS when the target shares the caller's
// address space -- see procVmRead/procVmWrite.

const (
	lxProcessVmReadv  = 270
	lxProcessVmWritev = 271

	iovMax   = 1024 // UIO_MAXIOV: the kernel's iovec-count cap
	ssizeMax = int64(^uint64(0) >> 1)
)

func init() {
	regSys(lxProcessVmReadv, func(g *dguest, a [6]uint64) int64 { return g.processVM(a, false) })
	regSys(lxProcessVmWritev, func(g *dguest, a [6]uint64) int64 { return g.processVM(a, true) })
}

// processVM serves both calls: write=false reads the target's remote_iov into
// the caller's local_iov (readv), write=true copies the caller's local_iov to
// the target's remote_iov (writev). It moves min(local, remote) total bytes as
// a flat stream across each side's iovecs, stopping at the first fault with the
// bytes already moved (a short count, as Linux).
func (g *dguest) processVM(a [6]uint64, write bool) int64 {
	liovcnt, riovcnt, flags := a[2], a[4], a[5]
	if flags != 0 || liovcnt > iovMax || riovcnt > iovMax {
		return sErr(lxEINVAL)
	}
	// The caller reading or writing its own address space is always valid and
	// needs no process-table lookup (the main process is not always listed).
	target := g
	if tpid := int(int32(a[0])); tpid != g.nsPid(g.tgid) {
		gpid, ok := g.nsToGlobal(tpid)
		if !ok {
			return sErr(lxESRCH)
		}
		g.dfs.mu.Lock()
		dp := g.dfs.procs[gpid]
		// process_vm_readv/writev reach another process's memory, so they take
		// the same ptrace-attach credential check ptrace and pidfd_getfd do; a
		// guest process must not read or write an unrelated one's memory.
		may := dp != nil && dp.g != nil && ptraceMayAccess(g.ps, dp.g.ps)
		g.dfs.mu.Unlock()
		if dp == nil || dp.g == nil {
			return sErr(lxESRCH)
		}
		if !may {
			return sErr(lxEPERM)
		}
		target = dp.g
	}

	local, e := g.iovecs(a[1], int64(liovcnt))
	if e != 0 {
		return sErr(e)
	}
	remote, e := g.iovecs(a[3], int64(riovcnt))
	if e != 0 {
		return sErr(e)
	}
	lt, rt := int64(iovTotal(local)), int64(iovTotal(remote))
	if lt > ssizeMax || rt > ssizeMax {
		return sErr(lxEINVAL)
	}
	total := int(min(lt, rt))
	if total == 0 {
		return 0
	}
	moved, fe := g.vmStream(target, local, remote, total, write)
	if moved == 0 && fe != 0 {
		return sErr(fe)
	}
	return int64(moved)
}

// vmStream copies up to total bytes between the caller's local iovecs and the
// target's remote iovecs as two flat streams, advancing only by bytes actually
// delivered. On a fault it stops and reports the bytes moved so far, with the
// faulting errno for the zero-progress case.
func (g *dguest) vmStream(target *dguest, local, remote [][2]uint64, total int, write bool) (int, lxErrno) {
	const maxChunk = 1 << 16 // move at most this much per channel round trip
	moved := 0
	li, ri := 0, 0
	var lo, ro uint64 // offset within the current local/remote iovec
	var fault lxErrno
	for moved < total {
		for li < len(local) && lo >= local[li][1] {
			li++
			lo = 0
		}
		for ri < len(remote) && ro >= remote[ri][1] {
			ri++
			ro = 0
		}
		if li >= len(local) || ri >= len(remote) {
			break
		}
		n := min(min(int(local[li][1]-lo), int(remote[ri][1]-ro)), min(total-moved, maxChunk))
		if n == 0 {
			break
		}
		lAddr, rAddr := local[li][0]+lo, remote[ri][0]+ro
		if write { // src = caller's local, dst = target's remote
			data, err := g.readMem(lAddr, n)
			if err != nil {
				fault = lxEFAULT
				break
			}
			if e := procVmWrite(g, target, rAddr, data); e != 0 {
				fault = e
				break
			}
		} else { // src = target's remote, dst = caller's local
			data, e := procVmRead(g, target, rAddr, n)
			if e != 0 {
				fault = e
				break
			}
			if g.writeMem(lAddr, data) != nil {
				fault = lxEFAULT
				break
			}
		}
		moved += n
		lo += uint64(n)
		ro += uint64(n)
	}
	return moved, fault
}
