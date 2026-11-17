// ptraceseize: PTRACE_SEIZE, PTRACE_INTERRUPT and PTRACE_LISTEN, and attaching
// to a task stopped by job control, as Linux has them.
//
//   read     a seized child blocked in read() traps (PTRACE_EVENT_STOP,
//            SIGTRAP) on INTERRUPT; continued, its read restarts and completes.
//   busy     a seized child computing in user space traps on INTERRUPT.
//   listen   a seized child's group stop is an EVENT_STOP with the stop signal;
//            PTRACE_LISTEN holds it, and SIGCONT re-traps it with SIGTRAP.
//   stopped  ATTACH to a job-stopped child reports its group stop, then the
//            attach's own SIGSTOP; SEIZE reports an EVENT_STOP with SIGSTOP,
//            and after SIGCONT another with SIGTRAP before SIGCONT itself.
//   fork     a seized child's new child starts in an EVENT_STOP with SIGTRAP.
//   exec     a seized child execs with no SIGTRAP stop.
//   stopped-mt (only when named) SEIZE one thread of a job-stopped process
//            whose other thread spins: it reports an EVENT_STOP with SIGSTOP
//            at once, without a SIGCONT, while the other thread stays
//            stopped; detached, the process is still stopped until SIGCONT.
#define _GNU_SOURCE
#include <errno.h>
#include <pthread.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/ptrace.h>
#include <sys/uio.h>
#include <sys/wait.h>
#include <unistd.h>

#ifndef PTRACE_EVENT_STOP
#define PTRACE_EVENT_STOP 128
#endif

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %s:%d: ", __func__, __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

// trapped is the status of a PTRACE_EVENT_STOP with sig.
static int trapped(int sig) { return (sig | PTRACE_EVENT_STOP << 8) << 8 | 0x7f; }

// reader forks a child that exits 5 once it reads 'x' from the pipe.
static pid_t reader(int p[2]) {
	if (pipe(p) != 0)
		return -1;
	pid_t c = fork();
	if (c == 0) {
		char b;
		close(p[1]);
		ssize_t n = read(p[0], &b, 1);
		_exit(n == 1 && b == 'x' ? 5 : 6);
	}
	close(p[0]);
	return c;
}

static void t_read(void) {
	int p[2];
	pid_t c = reader(p);
	CHECK(ptrace(PTRACE_SEIZE, c, 0, 0) == 0, "seize: %s", strerror(errno));
	CHECK(ptrace(PTRACE_INTERRUPT, c, 0, 0) == 0, "interrupt: %s", strerror(errno));
	int st;
	waitpid(c, &st, 0);
	CHECK(st == trapped(SIGTRAP), "interrupt stop %#x", st);
	siginfo_t si;
	CHECK(ptrace(PTRACE_GETSIGINFO, c, 0, &si) == 0 && si.si_signo == SIGTRAP && si.si_code == (SIGTRAP | PTRACE_EVENT_STOP << 8),
		"siginfo: %s signo %d code %#x", strerror(errno), si.si_signo, si.si_code);
	ptrace(PTRACE_CONT, c, 0, 0);
	CHECK(write(p[1], "x", 1) == 1, "write");
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 5, "the read did not restart: %#x", st);
}

static void t_busy(void) {
	pid_t c = fork();
	if (c == 0) {
		volatile unsigned long n = 0;
		for (;;)
			n++;
	}
	CHECK(ptrace(PTRACE_SEIZE, c, 0, 0) == 0, "seize: %s", strerror(errno));
	CHECK(ptrace(PTRACE_INTERRUPT, c, 0, 0) == 0, "interrupt: %s", strerror(errno));
	int st;
	waitpid(c, &st, 0);
	CHECK(st == trapped(SIGTRAP), "interrupt stop %#x", st);
	kill(c, SIGKILL);
	waitpid(c, &st, 0);
	CHECK(WIFSIGNALED(st) && WTERMSIG(st) == SIGKILL, "kill %#x", st);
}

static void t_listen(void) {
	int p[2];
	pid_t c = reader(p);
	ptrace(PTRACE_SEIZE, c, 0, 0);
	kill(c, SIGSTOP);
	int st;
	waitpid(c, &st, 0);
	CHECK(WIFSTOPPED(st) && st >> 8 == SIGSTOP, "SIGSTOP delivery stop %#x", st);
	ptrace(PTRACE_CONT, c, 0, SIGSTOP);
	waitpid(c, &st, 0);
	CHECK(st == trapped(SIGSTOP), "group stop %#x", st);
	CHECK(ptrace(PTRACE_LISTEN, c, 0, 0) == 0, "listen: %s", strerror(errno));
	CHECK(waitpid(c, &st, WNOHANG) == 0, "listening, it reported %#x", st);
	kill(c, SIGCONT);
	waitpid(c, &st, 0);
	CHECK(st == trapped(SIGTRAP), "SIGCONT re-trap %#x", st);
	ptrace(PTRACE_CONT, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(WIFSTOPPED(st) && st >> 8 == SIGCONT, "SIGCONT delivery stop %#x", st);
	ptrace(PTRACE_CONT, c, 0, SIGCONT);
	CHECK(write(p[1], "x", 1) == 1, "write");
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 5, "ended %#x", st);
}

// stopper forks a child that stops itself and, once continued, exits 4.
static pid_t stopper(void) {
	pid_t c = fork();
	if (c == 0) {
		raise(SIGSTOP);
		_exit(4);
	}
	int st;
	waitpid(c, &st, WUNTRACED);
	return WIFSTOPPED(st) ? c : -1;
}

static void t_stopped(void) {
	pid_t c = stopper();
	CHECK(c > 0, "the child did not stop");
	CHECK(ptrace(PTRACE_ATTACH, c, 0, 0) == 0, "attach: %s", strerror(errno));
	int st;
	waitpid(c, &st, 0);
	siginfo_t si;
	CHECK(WIFSTOPPED(st) && st >> 8 == SIGSTOP && ptrace(PTRACE_GETSIGINFO, c, 0, &si) == -1 && errno == EINVAL,
		"group stop %#x (siginfo: errno %d)", st, errno);
	ptrace(PTRACE_CONT, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(WIFSTOPPED(st) && st >> 8 == SIGSTOP && ptrace(PTRACE_GETSIGINFO, c, 0, &si) == 0,
		"the attach's SIGSTOP %#x", st);
	ptrace(PTRACE_CONT, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 4, "attached ended %#x", st);

	c = stopper();
	CHECK(ptrace(PTRACE_SEIZE, c, 0, 0) == 0, "seize: %s", strerror(errno));
	waitpid(c, &st, 0);
	CHECK(st == trapped(SIGSTOP), "seized group stop %#x", st);
	kill(c, SIGCONT);
	ptrace(PTRACE_CONT, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(st == trapped(SIGTRAP), "SIGCONT re-trap %#x", st);
	ptrace(PTRACE_CONT, c, 0, 0);
	waitpid(c, &st, 0);
	CHECK(WIFSTOPPED(st) && st >> 8 == SIGCONT, "SIGCONT delivery stop %#x", st);
	ptrace(PTRACE_CONT, c, 0, SIGCONT);
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 4, "seized ended %#x", st);
}

static volatile unsigned long *spins;

static void *spinner(void *a) {
	(void)a;
	for (;;)
		(*spins)++;
	return 0;
}

// stopped reports whether process c is stopped, from its /proc state.
static int stopped(pid_t c) {
	char path[64], buf[256];
	snprintf(path, sizeof path, "/proc/%d/stat", c);
	FILE *f = fopen(path, "r");
	if (!f)
		return 0;
	size_t n = fread(buf, 1, sizeof buf - 1, f);
	fclose(f);
	buf[n] = 0;
	char *e = strrchr(buf, ')');
	return e && e[1] == ' ' && (e[2] == 'T' || e[2] == 't');
}

static void t_stopped_mt(void) {
	spins = mmap(0, 4096, PROT_READ | PROT_WRITE, MAP_SHARED | MAP_ANONYMOUS, -1, 0);
	pid_t c = fork();
	if (c == 0) {
		pthread_t t;
		pthread_create(&t, 0, spinner, 0);
		while (*spins == 0)
			;
		raise(SIGSTOP);
		_exit(4);
	}
	int st;
	waitpid(c, &st, WUNTRACED);
	CHECK(WIFSTOPPED(st), "the child did not stop: %#x", st);
	CHECK(stopped(c), "/proc does not show the stopped child stopped");
	unsigned long before = *spins;
	CHECK(ptrace(PTRACE_SEIZE, c, 0, 0) == 0, "seize: %s", strerror(errno));
	waitpid(c, &st, __WALL);
	CHECK(st == trapped(SIGSTOP), "seized group stop %#x", st);
	unsigned long regs[64];
	struct iovec iov = {regs, sizeof regs};
	CHECK(ptrace(PTRACE_GETREGSET, c, (void *)1, &iov) == 0, "regs: %s", strerror(errno)); // NT_PRSTATUS
	CHECK(*spins == before, "the other thread ran while stopped: %lu -> %lu", before, *spins);
	CHECK(ptrace(PTRACE_DETACH, c, 0, 0) == 0, "detach: %s", strerror(errno));
	CHECK(stopped(c), "detached, the process is no longer stopped");
	CHECK(*spins == before, "the other thread ran after the detach: %lu -> %lu", before, *spins);
	kill(c, SIGCONT);
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 4, "ended %#x", st);
}

static void t_fork(void) {
	int p[2];
	if (pipe(p) != 0)
		return;
	pid_t c = fork();
	if (c == 0) {
		char b;
		close(p[1]);
		if (read(p[0], &b, 1) != 1)
			_exit(9);
		pid_t g = fork();
		if (g == 0)
			_exit(3);
		int st;
		waitpid(g, &st, 0);
		_exit(WIFEXITED(st) && WEXITSTATUS(st) == 3 ? 0 : 1);
	}
	close(p[0]);
	CHECK(ptrace(PTRACE_SEIZE, c, 0, PTRACE_O_TRACEFORK) == 0, "seize: %s", strerror(errno));
	CHECK(write(p[1], "f", 1) == 1, "write");
	pid_t g = 0;
	int gfirst = -1, cst = -1, gst = -1;
	while (cst == -1 || gst == -1) {
		int st;
		pid_t w = waitpid(-1, &st, __WALL);
		if (w < 0)
			break;
		if (WIFEXITED(st) || WIFSIGNALED(st)) {
			if (w == c)
				cst = st;
			else
				gst = st;
			continue;
		}
		if (w == c && st >> 8 == (SIGTRAP | PTRACE_EVENT_FORK << 8)) {
			unsigned long m = 0;
			ptrace(PTRACE_GETEVENTMSG, c, 0, &m);
			g = (pid_t)m;
			ptrace(PTRACE_CONT, c, 0, 0);
			continue;
		}
		if (w != c && gfirst == -1)
			gfirst = st;
		int sig = WSTOPSIG(st);
		ptrace(PTRACE_CONT, w, 0, (st >> 16) == 0 && sig != SIGTRAP ? sig : 0);
	}
	CHECK(g > 0, "no fork event");
	CHECK(gfirst == trapped(SIGTRAP), "the new child's first stop %#x", gfirst);
	CHECK(WIFEXITED(gst) && WEXITSTATUS(gst) == 3, "new child ended %#x", gst);
	CHECK(WIFEXITED(cst) && WEXITSTATUS(cst) == 0, "child ended %#x", cst);
}

static void t_exec(void) {
	int p[2];
	if (pipe(p) != 0)
		return;
	pid_t c = fork();
	if (c == 0) {
		char b;
		close(p[1]);
		if (read(p[0], &b, 1) != 1)
			_exit(9);
		execl("/proc/self/exe", "ptraceseize", "exec", (char *)0);
		_exit(99);
	}
	close(p[0]);
	ptrace(PTRACE_SEIZE, c, 0, 0);
	CHECK(write(p[1], "e", 1) == 1, "write");
	int st;
	waitpid(c, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 7, "seized exec %#x", st);
}

static const struct {
	const char *name;
	void (*run)(void);
	int named; // runs only when named
} cases[] = {
	{"read", t_read}, {"busy", t_busy}, {"listen", t_listen},
	{"stopped", t_stopped}, {"fork", t_fork}, {"exec", t_exec},
	{"stopped-mt", t_stopped_mt, 1},
};

// ptraceseize [case]: every case, or the one named.
int main(int argc, char **argv) {
	if (strcmp(argv[0], "ptraceseize") == 0 && argc > 1 && strcmp(argv[1], "exec") == 0)
		return 7; // t_exec's exec'd self
	for (size_t i = 0; i < sizeof cases / sizeof cases[0]; i++)
		if (argc < 2 ? !cases[i].named : strcmp(argv[1], cases[i].name) == 0)
			cases[i].run();
	printf("ptraceseize fails=%d\n", fails);
	return fails != 0;
}
