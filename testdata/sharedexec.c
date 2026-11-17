// sharedexec: code in shared memory makes syscalls as code from a file does,
// and runs what was last written to it: a raw syscall (lseek, whose result is
// the offset it is given, so no other call passes for it) written to a memfd
// with write(2) and mapped shared and executable; the same memfd mapped
// writable at one address and executable at another, rewritten while the
// executable page runs, a syscall added to that page later, a writable
// mapping made after it runs, and read(2) into the writable one; shared
// anonymous memory aliased by mremap with no old size; and code a fork child
// writes into shared memory, run by its parent once it has, then by a fork
// child, the memory still holding what was written; and threads that each
// write code into one shared page through writable mappings they make and
// unmap, and run it through the executable one.
#define _GNU_SOURCE
#include <fcntl.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/wait.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

#if defined(__x86_64__)
// mov eax, 8 (lseek); syscall; ret
static const unsigned char seekFn[] = {0xb8, 0x08, 0, 0, 0, 0x0f, 0x05, 0xc3};
// mov eax, k; ret
static void retFn(unsigned char *p, int k) {
	unsigned char f[] = {0xb8, (unsigned char)k, 0, 0, 0, 0xc3};
	memcpy(p, f, sizeof f);
}
#elif defined(__aarch64__)
// mov x8, #62 (lseek); svc #0; ret
static const unsigned int seekFn[] = {0xd28007c8, 0xd4000001, 0xd65f03c0};
// mov w0, #k; ret
static void retFn(unsigned char *p, int k) {
	unsigned int f[] = {0x52800000 | (unsigned)k << 5, 0xd65f03c0};
	memcpy(p, f, sizeof f);
}
#endif

typedef long (*fn)(long, long, long);

static int fd;
static long seq;

// seeks runs the lseek at code and reports whether it did seek.
static int seeks(void *code) {
	long off = ++seq * 4096 + 7;
	return ((fn)code)(fd, off, SEEK_SET) == off && lseek(fd, 0, SEEK_CUR) == off;
}

static long call(void *code) { return ((fn)code)(0, 0, 0); }

// syncCode makes n bytes of code written at p the code that runs there, as the
// architecture asks.
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

// The threads of churn share one page of a memfd, mapped executable at
// churnExec: each writes its own code into a slot of its own through a
// writable mapping it makes and unmaps each time, and runs it, as a JIT with
// a writable and an executable mapping of its code does.
static int churnFd;
static unsigned char *churnExec;
static long churnPage;

static void *churn(void *arg) {
	long t = (long)arg;
	for (int i = 0; i < 300; i++) {
		unsigned char *w = mmap(NULL, churnPage, PROT_READ | PROT_WRITE, MAP_SHARED, churnFd, 0);
		if (w == MAP_FAILED)
			return (void *)1;
		int k = (int)(t * 40 + i % 40);
		retFn(w + t * 64, k);
		munmap(w, churnPage);
		syncCode(churnExec + t * 64, 16);
		if (call(churnExec + t * 64) != k)
			return (void *)2;
	}
	return NULL;
}

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	long page = sysconf(_SC_PAGESIZE);
	char path[] = "/tmp/sharedexec-XXXXXX";
	fd = mkstemp(path);
	unlink(path);

	// One mapping: written with write(2), mapped shared and executable.
	int m = memfd_create("sharedexec", 0);
	CHECK(write(m, seekFn, sizeof seekFn) == sizeof seekFn, "write");
	ftruncate(m, page);
	void *x = mmap(NULL, page, PROT_READ | PROT_EXEC, MAP_SHARED, m, 0);
	CHECK(x != MAP_FAILED && seeks(x), "a syscall in a shared executable mapping did not seek");

	// Two mappings of one memfd: writable here, executable there.
	int d = memfd_create("dual", 0);
	ftruncate(d, page);
	unsigned char *w = mmap(NULL, page, PROT_READ | PROT_WRITE, MAP_SHARED, d, 0);
	unsigned char *e = mmap(NULL, page, PROT_READ | PROT_EXEC, MAP_SHARED, d, 0);
	retFn(w, 1);
	syncCode(e, 16);
	CHECK(call(e) == 1, "first code: %ld", call(e));
	retFn(w, 2); // over code that runs
	syncCode(e, 16);
	CHECK(call(e) == 2, "rewritten code: %ld", call(e));
	memcpy(w + 64, seekFn, sizeof seekFn); // a syscall added to the running page
	syncCode(e + 64, sizeof seekFn);
	CHECK(seeks(e + 64), "a syscall added to a running shared page did not seek");
	CHECK(call(e) == 2, "the page's other code: %ld", call(e));

	// A writable mapping made while the page runs.
	unsigned char *w2 = mmap(NULL, page, PROT_READ | PROT_WRITE, MAP_SHARED, d, 0);
	retFn(w2, 3);
	syncCode(e, 16);
	CHECK(call(e) == 3, "code written through a later mapping: %ld", call(e));

	// read(2) into the writable mapping while the page runs.
	unsigned char code[16];
	retFn(code, 4);
	int p[2];
	CHECK(pipe(p) == 0 && write(p[1], code, 8) == 8, "pipe");
	CHECK(read(p[0], w, 8) == 8, "read into a mapping of running code");
	syncCode(e, 16);
	CHECK(call(e) == 4, "code read in: %ld", call(e));

	// Shared anonymous memory, aliased by mremap with no old size.
	unsigned char *a = mmap(NULL, page, PROT_READ | PROT_WRITE, MAP_SHARED | MAP_ANONYMOUS, -1, 0);
	unsigned char *b = mremap(a, 0, page, MREMAP_MAYMOVE);
	CHECK(b != MAP_FAILED && mprotect(b, page, PROT_READ | PROT_EXEC) == 0, "alias");
	retFn(a, 5);
	syncCode(b, 16);
	CHECK(call(b) == 5, "code through an alias: %ld", call(b));
	retFn(a, 6);
	syncCode(b, 16);
	CHECK(call(b) == 6, "rewritten through an alias: %ld", call(b));

	// A fork child writes code into shared memory; the parent runs it after.
	unsigned char *s = mmap(NULL, page, PROT_READ | PROT_WRITE | PROT_EXEC, MAP_SHARED | MAP_ANONYMOUS, -1, 0);
	if (s != MAP_FAILED) {
		pid_t pid = fork();
		if (pid == 0) {
			memcpy(s, seekFn, sizeof seekFn);
			_exit(0);
		}
		waitpid(pid, NULL, 0);
		syncCode(s, sizeof seekFn);
		CHECK(seeks(s), "a syscall a fork child wrote did not seek");

		// A fork child runs it too, and the memory keeps what was written.
		unsigned char *r = mremap(s, 0, page, MREMAP_MAYMOVE);
		pid = fork();
		if (pid == 0)
			_exit(seeks(s) ? 0 : 1);
		int st = -1;
		waitpid(pid, &st, 0);
		CHECK(st == 0, "the fork child's syscall did not seek: %#x", st);
		CHECK(r != MAP_FAILED && memcmp(r, seekFn, sizeof seekFn) == 0, "the shared memory no longer holds the code written to it");
	}

	churnPage = page;
	churnFd = memfd_create("churn", 0);
	ftruncate(churnFd, page);
	churnExec = mmap(NULL, page, PROT_READ | PROT_EXEC, MAP_SHARED, churnFd, 0);
	pthread_t th[4];
	for (long t = 0; t < 4; t++)
		pthread_create(&th[t], NULL, churn, (void *)t);
	for (int t = 0; t < 4; t++) {
		void *r;
		pthread_join(th[t], &r);
		CHECK(r == NULL, "churning thread %d: %ld", t, (long)r);
	}

	printf("sharedexec fails=%d\n", fails);
	return fails != 0;
}
