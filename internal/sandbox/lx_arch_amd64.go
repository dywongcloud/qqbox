//go:build (darwin || linux || windows) && amd64

package sandbox

import "syscall"

const guestArch = "x86_64"

// Linux's x86-64 layout constants: ELF_ET_DYN_BASE, where a position-
// independent program with an interpreter loads, and vm.mmap_rnd_bits, the
// default page-count entropy of its randomized bases.
const (
	etDynBase    = 0x555555554000
	mmapRndBits  = 28
	stackRndBits = 22 // STACK_RND_MASK

	vmTaskSize  = 0x7ffffffff000 // the top of a 47-bit user address space
	unmappedDiv = 3              // TASK_UNMAPPED_BASE is TASK_SIZE / 3
	map32bit    = 0x40           // MAP_32BIT

	// unlimitedStackLegacy: an unlimited stack does not make the mmap layout
	// legacy (bottom-up) here, x86's mmap_is_legacy having no such case.
	unlimitedStackLegacy = false
)

// epollEventSize is sizeof(struct epoll_event), which is packed on x86-64.
const epollEventSize = 12

// stat is a Linux x86-64 struct stat.
func (s *lxStat) stat() []byte {
	var b [144]byte
	put64(b[0:], s.dev)
	put64(b[8:], s.ino)
	put64(b[16:], uint64(s.nlink))
	put32(b[24:], s.mode)
	put32(b[28:], s.uid)
	put32(b[32:], s.gid)
	put64(b[40:], s.rdev)
	put64(b[48:], uint64(s.size))
	put64(b[56:], uint64(s.blksize))
	put64(b[64:], uint64(s.blocks))
	for i, t := range []syscall.Timespec{s.atime, s.mtime, s.ctime} {
		put64(b[72+16*i:], uint64(t.Sec))
		put64(b[80+16*i:], uint64(t.Nsec))
	}
	return b[:]
}

// struct semid64_ds on x86-64: a padding word follows sem_otime and
// sem_ctime (asm/sembuf.h), so sem_nsems is at 80 and the struct is 104 bytes.
const (
	semdsCtime = 64
	semdsNsems = 80
	semdsLen   = 104
)
