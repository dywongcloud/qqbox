//go:build darwin || linux || windows

package sandbox

import (
	"time"
)

// lxUserHZ is the guest's USER_HZ (AT_CLKTCK): the unit of /proc tick counters.
const lxUserHZ = 100

type hostCPU struct {
	n        int    // logical CPUs
	eCores   int    // efficiency cores, numbered first (as Linux on Apple Silicon does)
	brand    string // machdep.cpu.brand_string
	features string // Linux cpuinfo feature names, in hwcap bit order
	bogomips float64
	partE    int // MIDR part numbers for the E and P cores
	partP    int
	mhzE     int // nominal max frequency, 0 when unknown
	mhzP     int
	memTotal uint64
}

// hostMem is a snapshot of host memory in bytes.
type hostMem struct {
	total, free, avail, active, inactive, wired, cached, anon, compressed uint64
	swapTotal, swapFree                                                   uint64
}

// taskInfo is a host process's usage (proc_pidinfo PROC_PIDTASKINFO).
type taskInfo struct {
	virt, rss       uint64
	utime, stime    time.Duration
	faults, pageins uint64
	csw             uint64
	threads         int
}

// cpuMHz is CPU i's nominal max frequency in MHz (0 if unknown).
func (h *hostCPU) cpuMHz(i int) int {
	if i < h.eCores {
		return h.mhzE
	}
	return h.mhzP
}

func (h *hostCPU) cpuPart(i int) int {
	if i < h.eCores {
		return h.partE
	}
	return h.partP
}
