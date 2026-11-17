//go:build (darwin || linux || windows) && arm64

package sandbox

import "syscall"

// The guest runs the host's architecture: this file and lx_arch_amd64.go hold
// what the shared layer does differently per guest ABI.

// epollEventSize is sizeof(struct epoll_event): its u64 data is aligned.
const epollEventSize = 16

const guestArch = "aarch64"

// Linux's arm64 layout constants (48-bit VA): ELF_ET_DYN_BASE, where a
// position-independent program with an interpreter loads, and
// vm.mmap_rnd_bits, the default page-count entropy of its randomized bases.
const (
	etDynBase    = 0xaaaaaaaaa000
	mmapRndBits  = 18
	stackRndBits = 18 // STACK_RND_MASK

	vmTaskSize  = 1 << 48 // the top of a 48-bit user address space
	unmappedDiv = 4       // TASK_UNMAPPED_BASE is TASK_SIZE / 4
	map32bit    = 0       // no MAP_32BIT

	// unlimitedStackLegacy: an unlimited stack makes the mmap layout legacy
	// (bottom-up), as the generic mmap_is_legacy has it.
	unlimitedStackLegacy = true
)

// stat is a Linux arm64 (asm-generic) struct stat.
func (s *lxStat) stat() []byte {
	var b [128]byte
	put64(b[0:], s.dev)
	put64(b[8:], s.ino)
	put32(b[16:], s.mode)
	put32(b[20:], s.nlink)
	put32(b[24:], s.uid)
	put32(b[28:], s.gid)
	put64(b[32:], s.rdev)
	put64(b[48:], uint64(s.size))
	put32(b[56:], s.blksize)
	put64(b[64:], uint64(s.blocks))
	for i, t := range []syscall.Timespec{s.atime, s.mtime, s.ctime} {
		put64(b[72+16*i:], uint64(t.Sec))
		put64(b[80+16*i:], uint64(t.Nsec))
	}
	return b[:]
}

// struct semid64_ds (asm-generic/sembuf.h): sem_otime, sem_ctime, sem_nsems
// after the 48-byte ipc64_perm, then two unused words.
const (
	semdsCtime = 56
	semdsNsems = 64
	semdsLen   = 88
)
