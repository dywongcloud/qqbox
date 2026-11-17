// crossexec: code in shared memory is what was last written to it, whoever
// wrote it: write(2) to the memfd reaches what runs; a fork child's store
// through its own mapping reaches what its parent runs, and the parent's
// what the child runs; and a child stopped while it ran the code neither
// keeps its parent from writing and running new code nor runs stale code
// once it goes on.
#define _GNU_SOURCE
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/wait.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

#if defined(__x86_64__)
// mov eax, k; ret
static void retFn(unsigned char *p, int k) {
	unsigned char f[] = {0xb8, (unsigned char)k, 0, 0, 0, 0xc3};
	memcpy(p, f, sizeof f);
}
#elif defined(__aarch64__)
// mov w0, #k; ret
static void retFn(unsigned char *p, int k) {
	unsigned int f[] = {0x52800000 | (unsigned)k << 5, 0xd65f03c0};
	memcpy(p, f, sizeof f);
}
#endif

// syncCode makes code written at p the code that runs there, as the
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

static unsigned char *x;

static int call(void) {
	syncCode(x, 16);
	return ((int (*)(void))x)();
}

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	long page = sysconf(_SC_PAGESIZE);
	int m = memfd_create("crossexec", 0);
	ftruncate(m, page);
	unsigned char *w = mmap(NULL, page, PROT_READ | PROT_WRITE, MAP_SHARED, m, 0);
	x = mmap(NULL, page, PROT_READ | PROT_EXEC, MAP_SHARED, m, 0);
	retFn(w, 1);
	CHECK(call() == 1, "first code: %d", call());

	// write(2) to the memfd while its page runs.
	unsigned char c[16];
	retFn(c, 2);
	CHECK(pwrite(m, c, 8, 0) == 8 && call() == 2, "code written with write(2): %d", call());

	int p2c[2], c2p[2];
	if (pipe(p2c) || pipe(c2p))
		return 2;
	pid_t pid = fork();
	if (pid == 0) {
		char b;
		// The child writes, its parent runs.
		for (int k = 10; k < 20; k++) {
			if (read(p2c[0], &b, 1) != 1)
				_exit(3);
			retFn(w, k);
			if (write(c2p[1], "w", 1) != 1)
				_exit(3);
		}
		// The parent writes, the child runs.
		for (int k = 30; k < 40; k++) {
			if (read(p2c[0], &b, 1) != 1)
				_exit(3);
			char r = call() == k ? 'y' : 'n';
			if (write(c2p[1], &r, 1) != 1)
				_exit(3);
		}
		// Stopped while it runs the code.
		if (read(p2c[0], &b, 1) != 1)
			_exit(3);
		call();
		if (write(c2p[1], "h", 1) != 1)
			_exit(3);
		raise(SIGSTOP);
		_exit(call() == 50 ? 0 : 1);
	}
	char b;
	for (int k = 10; k < 20; k++) {
		call();
		write(p2c[1], "r", 1);
		read(c2p[0], &b, 1);
		CHECK(call() == k, "code the child wrote: %d, want %d", call(), k);
	}
	for (int k = 30; k < 40; k++) {
		retFn(w, k);
		write(p2c[1], "w", 1);
		read(c2p[0], &b, 1);
		CHECK(b == 'y', "the child ran stale code for %d", k);
	}
	write(p2c[1], "s", 1);
	read(c2p[0], &b, 1);
	int st = 0;
	CHECK(waitpid(pid, &st, WUNTRACED) == pid && WIFSTOPPED(st), "the child did not stop: %#x", st);
	retFn(w, 45);
	CHECK(call() == 45, "code written while a child that ran it is stopped: %d", call());
	retFn(c, 50);
	CHECK(pwrite(m, c, 8, 0) == 8 && call() == 50, "write(2) while it is stopped: %d", call());
	kill(pid, SIGCONT);
	CHECK(waitpid(pid, &st, 0) == pid && WIFEXITED(st) && WEXITSTATUS(st) == 0, "the child, gone on, ran stale code: %#x", st);

	printf("crossexec fails=%d\n", fails);
	return fails != 0;
}
