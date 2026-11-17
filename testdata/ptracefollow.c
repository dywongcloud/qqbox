// ptracefollow: a tracer follows its tracee through exec and fork, and attaches
// to and detaches from a running process.
//
//   exec   TRACEME + execve: a PEEKDATA at the execve entry stop leaves the
//          call's arguments alone, the exec event stop comes, and the
//          syscall-exit stop reports 0.
//   fork   with PTRACE_O_TRACEFORK the fork event names the new child, which
//          starts in a SIGSTOP stop and is traced to its exit.
//   attach PTRACE_ATTACH stops a child blocked in read() with SIGSTOP;
//          after PTRACE_DETACH its read completes and it exits normally.
//   held   a process traced by its grandparent exits: the exit goes to the
//          tracer first, and its parent, blocked in wait4 with a SIGCHLD
//          handler that does not restart calls, learns of it only when the
//          tracer reaps it or ends -- its wait4 reaps it, never EINTR.
#define _GNU_SOURCE
#include <errno.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/ptrace.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

#ifndef PTRACE_GET_SYSCALL_INFO
#define PTRACE_GET_SYSCALL_INFO 0x420e
#endif

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %s:%d: ", __func__, __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

static char marker[16] = "ptracefollow";

struct sysinfo_exit {
	uint8_t op;
	uint8_t pad[3];
	uint32_t arch;
	uint64_t ip, sp;
	int64_t rval; // at an entry stop, the syscall number
	uint8_t is_error;
};

static int stopsig(int st) { return WIFSTOPPED(st) ? WSTOPSIG(st) : -1; }

static void t_exec(const char *self) {
	pid_t c = fork();
	if (c == 0) {
		ptrace(PTRACE_TRACEME, 0, 0, 0);
		raise(SIGSTOP);
		execl("/proc/self/exe", self, "--exec", (char *)0);
		_exit(99);
	}
	int st;
	waitpid(c, &st, 0);
	CHECK(stopsig(st) == SIGSTOP, "first stop %#x", st);
	ptrace(PTRACE_SETOPTIONS, c, 0, PTRACE_O_TRACESYSGOOD | PTRACE_O_TRACEEXEC);
	// Run to the execve entry stop, past what raise() still calls.
	for (;;) {
		ptrace(PTRACE_SYSCALL, c, 0, 0);
		waitpid(c, &st, 0);
		if (stopsig(st) != (SIGTRAP | 0x80))
			break;
		struct sysinfo_exit si;
		memset(&si, 0, sizeof si);
		ptrace(PTRACE_GET_SYSCALL_INFO, c, sizeof si, &si);
		if (si.op == 1 && si.rval == SYS_execve) // PTRACE_SYSCALL_INFO_ENTRY
			break;
	}
	errno = 0;
	long w = ptrace(PTRACE_PEEKDATA, c, marker, 0);
	CHECK(errno == 0 && memcmp(&w, marker, sizeof w) == 0, "peek at the entry stop: %ld errno %d", w, errno);
	ptrace(PTRACE_SYSCALL, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(WIFSTOPPED(st) && st >> 8 == (SIGTRAP | PTRACE_EVENT_EXEC << 8), "exec event stop %#x", st);
	ptrace(PTRACE_SYSCALL, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(stopsig(st) == (SIGTRAP | 0x80), "exec exit stop %#x", st);
	struct sysinfo_exit si;
	memset(&si, 0, sizeof si);
	ptrace(PTRACE_GET_SYSCALL_INFO, c, sizeof si, &si);
	CHECK(si.op == 2 && si.rval == 0, "execve exit op %d rval %lld", si.op, (long long)si.rval);
	ptrace(PTRACE_CONT, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 7, "exec'd program ended %#x", st);
}

static void t_fork(const char *self) {
	pid_t c = fork();
	if (c == 0) {
		ptrace(PTRACE_TRACEME, 0, 0, 0);
		raise(SIGSTOP);
		execl("/proc/self/exe", self, "--forker", (char *)0);
		_exit(99);
	}
	int st;
	waitpid(c, &st, 0);
	CHECK(stopsig(st) == SIGSTOP, "first stop %#x", st);
	ptrace(PTRACE_SETOPTIONS, c, 0, PTRACE_O_TRACEFORK | PTRACE_O_TRACEEXEC);
	ptrace(PTRACE_CONT, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(WIFSTOPPED(st) && st >> 8 == (SIGTRAP | PTRACE_EVENT_EXEC << 8), "exec event stop %#x", st);
	ptrace(PTRACE_CONT, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(WIFSTOPPED(st) && st >> 8 == (SIGTRAP | PTRACE_EVENT_FORK << 8), "fork event stop %#x", st);
	unsigned long gc = 0;
	ptrace(PTRACE_GETEVENTMSG, c, 0, &gc);
	CHECK(gc > 0, "fork event names no child");
	int gst;
	pid_t w = waitpid((pid_t)gc, &gst, __WALL);
	CHECK(w == (pid_t)gc && stopsig(gst) == SIGSTOP, "new child's first stop: pid %d status %#x", w, gst);
	ptrace(PTRACE_CONT, (pid_t)gc, 0, 0);
	w = waitpid((pid_t)gc, &gst, __WALL);
	CHECK(w == (pid_t)gc && WIFEXITED(gst) && WEXITSTATUS(gst) == 3, "traced new child ended %#x", gst);
	// The forker sees its child's SIGCHLD as a signal-delivery stop.
	int sig = 0;
	for (;;) {
		ptrace(PTRACE_CONT, c, 0, sig);
		waitpid(c, &st, 0);
		if (!WIFSTOPPED(st))
			break;
		sig = WSTOPSIG(st);
	}
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0, "forker ended %#x", st);
}

static void t_attach(void) {
	int p[2];
	if (pipe(p) != 0)
		return;
	pid_t c = fork();
	if (c == 0) {
		char b;
		close(p[1]);
		_exit(read(p[0], &b, 1) == 1 && b == 'x' ? 5 : 6);
	}
	close(p[0]);
	CHECK(ptrace(PTRACE_ATTACH, c, 0, 0) == 0, "attach: %s", strerror(errno));
	int st;
	waitpid(c, &st, __WALL);
	CHECK(stopsig(st) == SIGSTOP, "attach stop %#x", st);
	errno = 0;
	long w = ptrace(PTRACE_PEEKDATA, c, marker, 0);
	CHECK(errno == 0 && memcmp(&w, marker, sizeof w) == 0, "peek after attach: errno %d", errno);
	CHECK(ptrace(PTRACE_DETACH, c, 0, 0) == 0, "detach: %s", strerror(errno));
	CHECK(write(p[1], "x", 1) == 1, "write");
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 5, "attached child ended %#x", st);
}

static void onchld(int s) { (void)s; }

static void t_held(int reap) {
	int gp[2], go[2], rep[2];
	if (pipe(gp) || pipe(go) || pipe(rep))
		return;
	prctl(PR_SET_CHILD_SUBREAPER, 1);
	pid_t a = fork();
	if (a == 0) {
		pid_t b = fork();
		if (b == 0) {
			struct sigaction sa;
			memset(&sa, 0, sizeof sa);
			sa.sa_handler = onchld;
			sigaction(SIGCHLD, &sa, 0);
			pid_t g = fork();
			if (g == 0) {
				char c;
				_exit(read(go[0], &c, 1) == 1 ? 4 : 5);
			}
			write(gp[1], &g, sizeof g);
			int st = 0;
			pid_t w = wait4(g, &st, 0, 0);
			int r[2] = {w == g && WIFEXITED(st) && WEXITSTATUS(st) == 4, w < 0 ? errno : 0};
			write(rep[1], r, sizeof r);
			_exit(0);
		}
		pid_t g;
		int st;
		if (read(gp[0], &g, sizeof g) != sizeof g)
			_exit(10);
		if (ptrace(PTRACE_ATTACH, g, 0, 0) != 0 || waitpid(g, &st, __WALL) != g || stopsig(st) != SIGSTOP)
			_exit(11);
		ptrace(PTRACE_CONT, g, 0, 0);
		write(go[1], "x", 1);
		siginfo_t si;
		if (waitid(P_PID, g, &si, WEXITED | WNOWAIT | __WALL) != 0)
			_exit(12);
		if (reap && (waitpid(g, &st, __WALL) != g || !WIFEXITED(st) || WEXITSTATUS(st) != 4))
			_exit(13);
		_exit(0); // without reaping, the tracer's end lets g go
	}
	int st;
	waitpid(a, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0, "tracer ended %#x", st);
	int r[2] = {0, 0};
	CHECK(read(rep[0], r, sizeof r) == sizeof r && r[0], "the parent's wait4: errno %d", r[1]);
	while (waitpid(-1, &st, 0) > 0)
		;
	prctl(PR_SET_CHILD_SUBREAPER, 0);
}

int main(int argc, char **argv) {
	if (argc > 1 && strcmp(argv[1], "--exec") == 0)
		return 7;
	if (argc > 1 && strcmp(argv[1], "--forker") == 0) {
		pid_t c = fork();
		if (c == 0)
			_exit(3);
		int st;
		waitpid(c, &st, 0);
		return WIFEXITED(st) && WEXITSTATUS(st) == 3 ? 0 : 1;
	}
	// ptracefollow [exec|fork|attach|held]: every case, or the one named.
	const char *only = argc > 1 ? argv[1] : "";
	if (!*only || !strcmp(only, "exec"))
		t_exec(argv[0]);
	if (!*only || !strcmp(only, "fork"))
		t_fork(argv[0]);
	if (!*only || !strcmp(only, "attach"))
		t_attach();
	if (!*only || !strcmp(only, "held")) {
		t_held(1);
		t_held(0);
	}
	printf("ptracefollow fails=%d\n", fails);
	return fails != 0;
}
