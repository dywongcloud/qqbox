// aslr: a program placed as Linux's ASLR places it: anew at each execve (the
// program, its stack and its heap move), the same across a fork, and not at
// all under personality(ADDR_NO_RANDOMIZE). Built position-dependent, the
// program's code stays at its link address while its stack and heap move.
#define _GNU_SOURCE
#include <elf.h>
#include <stdio.h>
#include <string.h>
#include <sys/personality.h>
#include <sys/wait.h>
#include <unistd.h>

extern const Elf64_Ehdr __ehdr_start; // this program's own ELF header

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %s:%d: ", __func__, __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

enum { CODE, STACK, HEAP, N };
static const char *what[N] = {"code", "stack", "heap"};

static void where(unsigned long w[N]) {
	volatile int local;
	w[CODE] = (unsigned long)&where;
	w[STACK] = (unsigned long)&local;
	w[HEAP] = (unsigned long)sbrk(0);
}

// placed reads where a fresh exec of this program (with ADDR_NO_RANDOMIZE
// when norand) finds itself.
static int placed(unsigned long w[N], int norand) {
	int p[2];
	if (pipe(p) != 0)
		return 0;
	pid_t c = fork();
	if (c == 0) {
		if (norand)
			personality(ADDR_NO_RANDOMIZE);
		dup2(p[1], 1);
		execl("/proc/self/exe", "aslr", "report", (char *)0);
		_exit(99);
	}
	close(p[1]);
	ssize_t n = read(p[0], w, N * sizeof w[0]);
	close(p[0]);
	int st;
	waitpid(c, &st, 0);
	return n == N * (ssize_t)sizeof w[0];
}

int main(int argc, char **argv) {
	unsigned long w[N];
	if (argc > 1 && strcmp(argv[1], "report") == 0) {
		where(w);
		return write(1, w, sizeof w) != sizeof w;
	}
	unsigned long a[N], b[N];
	CHECK(placed(a, 0) && placed(b, 0), "no report");
	for (int i = 0; i < N; i++) {
		if (i == CODE && __ehdr_start.e_type != ET_DYN) {
			CHECK(a[i] == b[i], "code moved from %#lx to %#lx", a[i], b[i]);
			continue;
		}
		CHECK(a[i] != b[i], "%s at %#lx after both execs", what[i], a[i]);
	}

	// After a fork the same objects are at the same addresses.
	volatile int marker;
	a[CODE] = (unsigned long)&where;
	a[STACK] = (unsigned long)&marker;
	a[HEAP] = (unsigned long)sbrk(0);
	int p[2];
	CHECK(pipe(p) == 0, "pipe");
	pid_t c = fork();
	if (c == 0) {
		b[CODE] = (unsigned long)&where;
		b[STACK] = (unsigned long)&marker;
		b[HEAP] = (unsigned long)sbrk(0);
		_exit(write(p[1], b, sizeof b) != sizeof b);
	}
	CHECK(read(p[0], b, sizeof b) == sizeof b, "fork report");
	waitpid(c, 0, 0);
	for (int i = 0; i < N; i++)
		CHECK(a[i] == b[i], "%s at %#lx, the forked child's at %#lx", what[i], a[i], b[i]);

	CHECK(placed(a, 1) && placed(b, 1), "no unrandomized report");
	for (int i = 0; i < N; i++)
		CHECK(a[i] == b[i], "unrandomized, %s at %#lx then %#lx", what[i], a[i], b[i]);
	printf("aslr fails=%d\n", fails);
	return fails != 0;
}
