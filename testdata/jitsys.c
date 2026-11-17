// jitsys: code a program writes and then runs makes syscalls as code from a
// file does: a function with a raw syscall (lseek, whose result is the offset
// it is given, so no other call passes for it) written to memory and made
// executable; written to memory mapped writable and executable at once, in two
// pages; written over code that already ran; read from a file into memory and
// run; code that writes into its own page goes on running; and a trap
// instruction of the program's own, in its text or in code it wrote, raises
// SIGILL rather than making a syscall.
#define _GNU_SOURCE
#include <fcntl.h>
#include <setjmp.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

#if defined(__x86_64__)
// mov eax, 39 (getpid); ud2; ret
static const unsigned char ownTrapFn[] = {0xb8, 0x27, 0, 0, 0, 0x0f, 0x0b, 0xc3};
#define OWN_TRAP_AT 5
static void ownTrap(void) { __asm__ volatile("mov $39, %%eax\n\tud2" ::: "rax"); }
// mov eax, 8 (lseek); syscall; ret
static const unsigned char seekFn[] = {0xb8, 0x08, 0, 0, 0, 0x0f, 0x05, 0xc3};
// inc dword [rip+1]; ret; then the dword it counts in
static const unsigned char bumpFn[] = {0xff, 0x05, 0x01, 0, 0, 0, 0xc3, 0, 0, 0, 0};
#define BUMP_COUNT 7
#elif defined(__aarch64__)
// mov x8, #172 (getpid); udf #0; ret
static const unsigned int ownTrapFn[] = {0xd2801588, 0, 0xd65f03c0};
#define OWN_TRAP_AT 4
static void ownTrap(void) { __asm__ volatile("mov x8, #172\n\t.inst 0" ::: "x8"); }
// mov x8, #62 (lseek); svc #0; ret
static const unsigned int seekFn[] = {0xd28007c8, 0xd4000001, 0xd65f03c0};
// adr x9, count; ldr w10, [x9]; add w10, w10, #1; str w10, [x9]; ret; count
static const unsigned int bumpFn[] = {0x100000a9, 0xb940012a, 0x1100054a, 0xb900012a, 0xd65f03c0, 0};
#define BUMP_COUNT 20
#endif

typedef long (*fn)(long, long, long);

static int fd;
static long seq;

// seeks runs the lseek at code and reports whether it did seek.
static int seeks(void *code) {
	long off = ++seq * 4096 + 7;
	return ((fn)code)(fd, off, SEEK_SET) == off && lseek(fd, 0, SEEK_CUR) == off;
}

// syncCode makes n bytes of code written at p the code that runs there, as the
// architecture asks. Zig's compiler-rt __clear_cache leaves out the dsb
// after ic ivau, and a CPU may then run the bytes that were there before.
static void syncCode(void *p, size_t n) {
#if defined(__aarch64__)
	unsigned long ctr;
	__asm__ volatile("mrs %0, ctr_el0" : "=r"(ctr));
	unsigned long d = 4UL << (ctr >> 16 & 15), i = 4UL << (ctr & 15);
	unsigned long lo = (unsigned long)p, hi = lo + n;
	if (!(ctr >> 28 & 1))
		for (unsigned long a = lo & ~(d - 1); a < hi; a += d)
			__asm__ volatile("dc cvau, %0" ::"r"(a) : "memory");
	__asm__ volatile("dsb ish" ::: "memory");
	if (!(ctr >> 29 & 1))
		for (unsigned long a = lo & ~(i - 1); a < hi; a += i)
			__asm__ volatile("ic ivau, %0" ::"r"(a) : "memory");
	__asm__ volatile("dsb ish\n\tisb" ::: "memory");
#else
	(void)p, (void)n;
#endif
}

static sigjmp_buf ill;
static void *volatile illAt;

static void onIll(int sig, siginfo_t *si, void *uc) {
	(void)sig, (void)uc;
	illAt = si->si_addr;
	siglongjmp(ill, 1);
}

// raisesIll runs f and reports whether it raised SIGILL, and where.
static int raisesIll(void (*f)(void), void **at) {
	illAt = 0;
	if (sigsetjmp(ill, 1)) {
		*at = illAt;
		return 1;
	}
	f();
	return 0;
}

static void put(void *at, const void *code, size_t n) {
	memcpy(at, code, n);
	syncCode(at, n);
}

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	long pg = sysconf(_SC_PAGESIZE);
	char path[] = "/tmp/jitsysXXXXXX";
	fd = mkstemp(path);
	CHECK(fd >= 0, "mkstemp");
	unlink(path);

#if defined(__aarch64__)
	// An svc only a branch from another page shows to be one: the page that
	// branches runs first, the page with the svc is entered elsewhere, and
	// only then does the branch reach the svc.
	{
		unsigned char *a = mmap(0, 2 * pg, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
		unsigned int *A = (unsigned int *)a, *B = (unsigned int *)(a + pg);
		for (int i = 0; i < 16; i++)
			B[i] = 0xd65f03c0; // ret
		B[16] = 0xd4000001;        // svc #0
		B[17] = 0xd65f03c0;        // ret
		A[0] = 0xb4000063;         // cbz x3, A+12
		A[1] = 0xd28007c8;         // mov x8, #62 (lseek)
		A[2] = 0x14000000 | (unsigned int)(B + 16 - (A + 2)); // b B+64
		A[3] = 0xd65f03c0;         // ret
		syncCode(a, 2 * pg);
		CHECK(mprotect(a, 2 * pg, PROT_READ | PROT_EXEC) == 0, "mprotect RX");
		typedef long (*fn4)(long, long, long, long);
		((fn4)A)(0, 0, 0, 0);
		((void (*)(void))B)();
		long off = ++seq * 4096 + 7;
		CHECK(((fn4)A)(fd, off, SEEK_SET, 1) == off && lseek(fd, 0, SEEK_CUR) == off, "lseek reached only from another page");
	}
#endif

	// Written, then made executable.
	unsigned char *m = mmap(0, pg, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	put(m, seekFn, sizeof seekFn);
	CHECK(mprotect(m, pg, PROT_READ | PROT_EXEC) == 0, "mprotect RX");
	CHECK(seeks(m), "lseek from code made executable");

	// Writable and executable at once, in two pages; the second page's
	// function runs first.
	unsigned char *x = mmap(0, 2 * pg, PROT_READ | PROT_WRITE | PROT_EXEC, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	CHECK(x != MAP_FAILED, "mmap RWX");
	put(x + pg + 64, seekFn, sizeof seekFn);
	put(x, seekFn, sizeof seekFn);
	CHECK(seeks(x + pg + 64), "lseek from the second RWX page");
	CHECK(seeks(x), "lseek from the first RWX page");

	// Code written over code that ran, then run: a page written after it
	// ran is rewritten before it runs again.
	put(x + 256, seekFn, sizeof seekFn);
	CHECK(seeks(x + 256), "lseek written after the page ran");
	memset(x, 0xcc, 256); // the first function gone
	put(x + 512, seekFn, sizeof seekFn);
	CHECK(seeks(x + 512), "lseek written over code that ran");

	// Read from a file, as a loader does.
	CHECK(pwrite(fd, seekFn, sizeof seekFn, 0) == (ssize_t)sizeof seekFn, "write the code to a file");
	unsigned char *l = mmap(0, pg, PROT_READ | PROT_WRITE | PROT_EXEC, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	CHECK(seeks(x + 512), "a page ran before the read into it");
	CHECK(pread(fd, x + 1024, sizeof seekFn, 0) == (ssize_t)sizeof seekFn, "read code into a page that ran");
	syncCode(x + 1024, sizeof seekFn);
	CHECK(seeks(x + 1024), "lseek read from a file");
	CHECK(pread(fd, l, sizeof seekFn, 0) == (ssize_t)sizeof seekFn, "read code into fresh memory");
	syncCode(l, sizeof seekFn);
	CHECK(seeks(l), "lseek read from a file into fresh memory");

	// A trap instruction of the program's own raises SIGILL, in its text, in
	// code it wrote, and beside a syscall of code it wrote that ran trapped.
	struct sigaction sa = {0};
	sa.sa_sigaction = onIll;
	sa.sa_flags = SA_SIGINFO | SA_NODEFER;
	sigaction(SIGILL, &sa, 0);
	void *at = 0;
	CHECK(raisesIll(ownTrap, &at), "a trap in the program's text raised no SIGILL");
	unsigned char *t = mmap(0, pg, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	put(t, ownTrapFn, sizeof ownTrapFn);
	put(t + 256, seekFn, sizeof seekFn);
	CHECK(mprotect(t, pg, PROT_READ | PROT_EXEC) == 0, "mprotect RX");
	CHECK(seeks(t + 256), "lseek beside a trap of the program's own");
	CHECK(raisesIll((void (*)(void))t, &at) && at == t + OWN_TRAP_AT, "a trap in code written raised no SIGILL at it (%p, not %p)", at, t + OWN_TRAP_AT);
	CHECK(seeks(t + 256), "lseek after the SIGILL beside it");

	// Code that writes into its own page goes on running.
	unsigned char *s = mmap(0, pg, PROT_READ | PROT_WRITE | PROT_EXEC, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	put(s, bumpFn, sizeof bumpFn);
	for (int i = 0; i < 3; i++)
		((void (*)(void))s)();
	CHECK(*(volatile int *)(s + BUMP_COUNT) == 3, "self-modifying code ran: count %d", *(volatile int *)(s + BUMP_COUNT));

	printf("jitsys fails=%d\n", fails);
	return fails != 0;
}
