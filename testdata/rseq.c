// rseq: restartable sequences work as on Linux. glibc registers its area at
// startup, so registering it again is EBUSY; the kernel keeps its cpu_id, a
// CPU the thread may run on; a fork child inherits the registration (EBUSY
// there too); and an execve ends it, so the new program registers afresh.
#define _GNU_SOURCE
#include <errno.h>
#include <sched.h>
#include <stdio.h>
#include <string.h>
#include <sys/rseq.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

static struct rseq *area(void) { return (struct rseq *)((char *)__builtin_thread_pointer() + __rseq_offset); }

// registered checks the thread's area is registered and kept.
static void registered(const char *who) {
	CHECK(__rseq_size > 0, "%s: glibc did not register rseq", who);
	if (__rseq_size == 0)
		return;
	// glibc registers the area at the kernel's feature size, at least 32.
	long r = syscall(SYS_rseq, area(), __rseq_size > 32 ? __rseq_size : 32, 0, RSEQ_SIG);
	CHECK(r == -1 && errno == EBUSY, "%s: registering again gave %ld (%s), want EBUSY", who, r, strerror(errno));
	cpu_set_t set;
	sched_getaffinity(0, sizeof set, &set);
	int c = (int)area()->cpu_id;
	CHECK(c >= 0 && CPU_ISSET(c, &set), "%s: cpu_id %d is not a CPU the thread may run on", who, c);
	c = sched_getcpu();
	CHECK(c >= 0 && CPU_ISSET(c, &set), "%s: sched_getcpu %d", who, c);
}

int main(int argc, char **argv) {
	if (argc > 1 && strcmp(argv[1], "exec") == 0) {
		registered("after execve");
		return fails != 0;
	}
	registered("main");
	pid_t c = fork();
	if (c == 0) {
		registered("fork child");
		_exit(fails != 0);
	}
	int st;
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0, "the fork child");
	c = fork();
	if (c == 0) {
		execl("/proc/self/exe", "rseq", "exec", (char *)0);
		_exit(99);
	}
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0, "the exec'd program: status %#x", st);
	printf("rseq fails=%d\n", fails);
	return fails != 0;
}
