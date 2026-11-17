// ptracethreads: a tracer follows a traced process's threads. With
// PTRACE_O_TRACECLONE each pthread_create reports a clone event naming the new
// thread, which starts in a SIGSTOP stop (the two in either order); each thread
// is seen to end, and the process (whose first threads end before the last are
// made, so their slots are reused) joins them all and exits 0.
#define _GNU_SOURCE
#include <errno.h>
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/ptrace.h>
#include <sys/wait.h>
#include <unistd.h>

#define THREADS 4

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %s:%d: ", __func__, __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

static void *work(void *a) {
	getppid();
	return a;
}

// threads runs THREADS threads, each ending at once, and joins them.
static int threads(void) {
	pthread_t t[THREADS];
	long sum = 0;
	for (long i = 0; i < THREADS; i++)
		if (pthread_create(&t[i], 0, work, (void *)i) != 0)
			return 1;
	for (int i = 0; i < THREADS; i++) {
		void *r;
		pthread_join(t[i], &r);
		sum += (long)r;
	}
	return sum == THREADS * (THREADS - 1) / 2 ? 0 : 2;
}

int main(int argc, char **argv) {
	if (argc > 1 && strcmp(argv[1], "threads") == 0)
		return threads();
	pid_t c = fork();
	if (c == 0) {
		ptrace(PTRACE_TRACEME, 0, 0, 0);
		raise(SIGSTOP);
		execl("/proc/self/exe", argv[0], "threads", (char *)0);
		_exit(99);
	}
	int st;
	waitpid(c, &st, 0);
	ptrace(PTRACE_SETOPTIONS, c, 0, PTRACE_O_TRACECLONE);
	ptrace(PTRACE_CONT, c, 0, 0);
	int clones = 0, starts = 0, ends = 0, code = -1;
	for (;;) {
		pid_t w = waitpid(-1, &st, __WALL);
		if (w < 0)
			break;
		if (WIFEXITED(st) || WIFSIGNALED(st)) {
			if (w == c) {
				code = st;
				break;
			}
			ends++;
			continue;
		}
		int sig = WSTOPSIG(st), inject = 0;
		if (w == c && st >> 8 == (SIGTRAP | PTRACE_EVENT_CLONE << 8)) {
			unsigned long m = 0;
			CHECK(ptrace(PTRACE_GETEVENTMSG, c, 0, &m) == 0 && m > 0, "clone event names no thread");
			clones++;
		} else if (w != c && sig == SIGSTOP) {
			starts++; // its first stop: the parent's clone event may come before or after it
		} else if ((st >> 16) == 0 && sig != SIGTRAP && sig != SIGSTOP) {
			inject = sig;
		}
		ptrace(PTRACE_CONT, w, 0, inject);
	}
	CHECK(clones == THREADS, "clone events %d", clones);
	CHECK(starts == THREADS, "thread starts %d", starts);
	CHECK(ends == THREADS, "thread ends %d", ends);
	CHECK(WIFEXITED(code) && WEXITSTATUS(code) == 0, "process ended %#x", code);
	printf("ptracethreads fails=%d\n", fails);
	return fails != 0;
}
