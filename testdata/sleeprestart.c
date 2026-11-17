// sleeprestart: a sleep a signal interrupts goes on or fails as on Linux.
//
//   attach  a tracer attaches mid-sleep and continues the sleeper without a
//           signal: the sleep finishes (0), and the tracer's next syscall stop
//           is restart_syscall resuming it (or the sleep itself, when the
//           attach came first).
//   stop    SIGSTOP then SIGCONT mid-sleep: relative and absolute sleeps both
//           finish (0).
//   handler a handler with SA_RESTART runs mid-sleep: a relative sleep fails
//           with EINTR and reports the time left, an absolute one fails with
//           EINTR; neither restarts.
//   bare    restart_syscall with nothing to restart fails with EINTR.
#define _GNU_SOURCE
#include <errno.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/ptrace.h>
#include <sys/syscall.h>
#include <sys/time.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#ifndef PTRACE_GET_SYSCALL_INFO
#define PTRACE_GET_SYSCALL_INFO 0x420e
#endif

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %s:%d: ", __func__, __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

struct sysinfo_entry {
	uint8_t op;
	uint8_t pad[3];
	uint32_t arch;
	uint64_t ip, sp;
	uint64_t nr;
};

static long now_ns(void) {
	struct timespec t;
	clock_gettime(CLOCK_MONOTONIC, &t);
	return t.tv_sec * 1000000000L + t.tv_nsec;
}

// sleeper forks a child that signals ready on a pipe and then sleeps 300 ms,
// relative or until an absolute time; it exits 0 if the sleep returned 0.
static pid_t sleeper(int absolute) {
	int p[2];
	if (pipe(p) != 0)
		return -1;
	pid_t c = fork();
	if (c == 0) {
		struct timespec t = {0, 300000000}, rem = {0, 0};
		long r;
		if (absolute) {
			clock_gettime(CLOCK_MONOTONIC, &t);
			t.tv_nsec += 300000000;
			if (t.tv_nsec >= 1000000000) {
				t.tv_sec++;
				t.tv_nsec -= 1000000000;
			}
			if (write(p[1], "r", 1) != 1)
				_exit(9);
			r = syscall(SYS_clock_nanosleep, CLOCK_MONOTONIC, TIMER_ABSTIME, &t, 0);
		} else {
			if (write(p[1], "r", 1) != 1)
				_exit(9);
			r = syscall(SYS_nanosleep, &t, &rem);
		}
		_exit(r == 0 ? 0 : 1);
	}
	char b;
	if (read(p[0], &b, 1) != 1)
		return -1;
	close(p[0]);
	close(p[1]);
	return c;
}

static void t_attach(void) {
	pid_t c = sleeper(0);
	int st;
	CHECK(ptrace(PTRACE_ATTACH, c, 0, 0) == 0, "attach: %s", strerror(errno));
	waitpid(c, &st, 0);
	CHECK(WIFSTOPPED(st) && WSTOPSIG(st) == SIGSTOP, "attach stop %#x", st);
	ptrace(PTRACE_SETOPTIONS, c, 0, PTRACE_O_TRACESYSGOOD);
	ptrace(PTRACE_SYSCALL, c, 0, 0);
	waitpid(c, &st, 0);
	struct sysinfo_entry si;
	memset(&si, 0, sizeof si);
	ptrace(PTRACE_GET_SYSCALL_INFO, c, sizeof si, &si);
	CHECK(WIFSTOPPED(st) && WSTOPSIG(st) == (SIGTRAP | 0x80) && si.op == 1 &&
		(si.nr == SYS_restart_syscall || si.nr == SYS_nanosleep),
		"next stop %#x op %d nr %llu", st, si.op, (unsigned long long)si.nr);
	ptrace(PTRACE_DETACH, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0, "the sleep did not finish: %#x", st);
}

static void t_stop(int absolute) {
	pid_t c = sleeper(absolute);
	int st;
	kill(c, SIGSTOP);
	waitpid(c, &st, WUNTRACED);
	CHECK(WIFSTOPPED(st), "absolute=%d: not stopped %#x", absolute, st);
	kill(c, SIGCONT);
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0, "absolute=%d: the sleep did not finish: %#x", absolute, st);
}

static void onalrm(int s) { (void)s; }

static void t_handler(void) {
	struct sigaction sa;
	memset(&sa, 0, sizeof sa);
	sa.sa_handler = onalrm;
	sa.sa_flags = SA_RESTART;
	sigaction(SIGALRM, &sa, 0);
	struct itimerval it = {{0, 0}, {0, 50000}};

	setitimer(ITIMER_REAL, &it, 0);
	struct timespec t = {10, 0}, rem = {0, 0};
	long start = now_ns();
	long r = syscall(SYS_nanosleep, &t, &rem);
	int e = errno;
	CHECK(r == -1 && e == EINTR, "relative: %ld errno %d", r, e);
	CHECK(rem.tv_sec > 0 && rem.tv_sec < 10, "relative: rem %ld.%09ld", (long)rem.tv_sec, rem.tv_nsec);
	CHECK(now_ns() - start < 5000000000L, "relative: restarted");

	setitimer(ITIMER_REAL, &it, 0);
	clock_gettime(CLOCK_MONOTONIC, &t);
	t.tv_sec += 10;
	start = now_ns();
	r = syscall(SYS_clock_nanosleep, CLOCK_MONOTONIC, TIMER_ABSTIME, &t, 0);
	e = errno;
	CHECK(r == -1 && e == EINTR, "absolute: %ld errno %d", r, e);
	CHECK(now_ns() - start < 5000000000L, "absolute: restarted");
	signal(SIGALRM, SIG_DFL);
}

static void t_bare(void) {
	errno = 0;
	long r = syscall(SYS_restart_syscall);
	CHECK(r == -1 && errno == EINTR, "%ld errno %d", r, errno);
}

int main(void) {
	t_attach();
	t_stop(0);
	t_stop(1);
	t_handler();
	t_bare();
	printf("sleeprestart fails=%d\n", fails);
	return fails != 0;
}
