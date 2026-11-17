//go:build windows

package sandbox

import (
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Host facts the synthetic /proc and /sys report. Per-process counters,
// per-CPU ticks and load averages come with the M3+ process work; until then
// they read as idle.

var (
	winHostOnce sync.Once
	winHost     hostCPU
)

type memoryStatusEx struct {
	length                                         uint32
	memoryLoad                                     uint32
	totalPhys, availPhys, totalPageFile, availPage uint64
	totalVirtual, availVirtual, availExtended      uint64
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

func memStatus() memoryStatusEx {
	m := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	return m
}

func hostCPUInfo() *hostCPU {
	winHostOnce.Do(func() {
		winHost = hostCPU{n: runtime.NumCPU(), brand: "x86_64", bogomips: 4000, memTotal: memStatus().totalPhys}
		if guestArch == "aarch64" {
			var names []string
			for _, f := range winHwcaps { // already in bit order
				if windows.IsProcessorFeaturePresent(f.pf) {
					names = append(names, f.name)
				}
			}
			winHost.features = strings.Join(names, " ")
		}
	})
	return &winHost
}

// winHwcaps maps a Windows processor feature to the Linux arm64 hwcap bit it
// stands for (0-63 AT_HWCAP, 64+ AT_HWCAP2), so the guest's auxv and
// /proc/cpuinfo say what the host has. FP and Advanced SIMD come with every
// ARMv8 Windows runs on. SVE and SME are left out: a signal frame carries no
// SVE or ZA state.
var winHwcaps = []struct {
	pf   uint32
	bit  int
	name string
}{
	{windows.PF_ARM_V8_INSTRUCTIONS_AVAILABLE, 0, "fp"},
	{windows.PF_ARM_V8_INSTRUCTIONS_AVAILABLE, 1, "asimd"},
	{windows.PF_ARM_V8_CRYPTO_INSTRUCTIONS_AVAILABLE, 3, "aes"},
	{windows.PF_ARM_V8_CRYPTO_INSTRUCTIONS_AVAILABLE, 4, "pmull"},
	{windows.PF_ARM_V8_CRYPTO_INSTRUCTIONS_AVAILABLE, 5, "sha1"},
	{windows.PF_ARM_V8_CRYPTO_INSTRUCTIONS_AVAILABLE, 6, "sha2"},
	{windows.PF_ARM_V8_CRC32_INSTRUCTIONS_AVAILABLE, 7, "crc32"},
	{windows.PF_ARM_V81_ATOMIC_INSTRUCTIONS_AVAILABLE, 8, "atomics"},
	{windows.PF_ARM_V82_FP16_INSTRUCTIONS_AVAILABLE, 9, "fphp"},
	{windows.PF_ARM_V82_FP16_INSTRUCTIONS_AVAILABLE, 10, "asimdhp"},
	{windows.PF_ARM_V83_JSCVT_INSTRUCTIONS_AVAILABLE, 13, "jscvt"},
	{windows.PF_ARM_V83_LRCPC_INSTRUCTIONS_AVAILABLE, 15, "lrcpc"},
	{windows.PF_ARM_SHA3_INSTRUCTIONS_AVAILABLE, 17, "sha3"},
	{windows.PF_ARM_V82_DP_INSTRUCTIONS_AVAILABLE, 20, "asimddp"},
	{windows.PF_ARM_SHA512_INSTRUCTIONS_AVAILABLE, 21, "sha512"},
	{windows.PF_ARM_LSE2_AVAILABLE, 25, "uscat"},
	{windows.PF_ARM_V82_I8MM_INSTRUCTIONS_AVAILABLE, 64 + 13, "i8mm"},
	{windows.PF_ARM_V86_BF16_INSTRUCTIONS_AVAILABLE, 64 + 14, "bf16"},
	{windows.PF_ARM_V86_EBF16_INSTRUCTIONS_AVAILABLE, 64 + 32, "ebf16"},
}

// hostHwcap is the guest's AT_HWCAP and AT_HWCAP2 on an arm64 host.
func hostHwcap() (hwcap, hwcap2 uint64) {
	for _, f := range winHwcaps {
		if !windows.IsProcessorFeaturePresent(f.pf) {
			continue
		}
		if f.bit < 64 {
			hwcap |= 1 << f.bit
		} else {
			hwcap2 |= 1 << (f.bit - 64)
		}
	}
	return hwcap, hwcap2
}

func cpuTicks() [][4]uint64 { return make([][4]uint64, hostCPUInfo().n) }

func memStats() hostMem {
	m := memStatus()
	return hostMem{total: m.totalPhys, free: m.availPhys, avail: m.availPhys,
		swapTotal: m.totalPageFile - m.totalPhys, swapFree: m.availPage}
}

func loadAvg() [3]float64 { return [3]float64{} }

var procGetTickCount64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

func bootTime() time.Time {
	ms, _, _ := procGetTickCount64.Call()
	return time.Now().Add(-time.Duration(ms) * time.Millisecond)
}

func pidTaskInfo(pid int) (taskInfo, bool) { return taskInfo{threads: 1}, pid > 0 }

func hostRusage(pid int) rusage { return rusage{} }
