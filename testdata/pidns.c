// pidns.c — exercises laboon's PID-namespace emulation: a clone(CLONE_NEWPID)
// child is pid 1 in its new namespace, its parent is invisible (getppid 0), a
// grandchild is pid 2, and the parent reaps the child by the pid clone handed
// back (which is not 1, since the parent is in the outer namespace).
#define _GNU_SOURCE
#include <fcntl.h>
#include <sched.h>
#include <stdio.h>
#include <unistd.h>
#include <sys/wait.h>
#include <sys/types.h>

static int pfd[2];

static int child_fn(void *arg) {
	pid_t self = getpid();
	pid_t parent = getppid();
	// /proc resolves in this namespace: self and its namespace pid 1 are both
	// us, and /proc/self/stat reports pid 1 (the namespace view).
	int b = open("/proc/1/stat", O_RDONLY);
	int statpid = -1;
	FILE *sf = fopen("/proc/self/stat", "r");
	if (sf) {
		if (fscanf(sf, "%d", &statpid) != 1) statpid = -1;
		fclose(sf);
	}
	int procOK = b >= 0 && statpid == 1;
	if (b >= 0) close(b);
	// A grandchild forked here is pid 2 in this namespace.
	pid_t grand = fork();
	if (grand == 0) {
		_exit(getpid() == 2 ? 0 : 1);
	}
	int st = 0;
	waitpid(grand, &st, 0);
	int grandOK = WIFEXITED(st) && WEXITSTATUS(st) == 0;
	int report[4] = {(int)self, (int)parent, grandOK, procOK};
	write(pfd[1], report, sizeof report);
	_exit(0);
}

static char stack[1 << 20];

int main(void) {
	if (pipe(pfd) != 0) return 10;
	pid_t kid = clone(child_fn, stack + sizeof stack, CLONE_NEWPID | SIGCHLD, 0);
	if (kid < 0) {
		perror("clone(CLONE_NEWPID)");
		return 11;
	}
	int report[4] = {0, 0, 0, 0};
	if (read(pfd[0], report, sizeof report) != (ssize_t)sizeof report) return 12;
	int st = 0;
	pid_t w = waitpid(kid, &st, 0);
	if (w != kid) {
		fprintf(stderr, "waitpid=%d want %d\n", w, kid);
		return 13;
	}
	if (report[0] != 1) {
		fprintf(stderr, "child getpid=%d want 1\n", report[0]);
		return 14;
	}
	if (report[1] != 0) {
		fprintf(stderr, "child getppid=%d want 0 (parent in outer ns)\n", report[1]);
		return 15;
	}
	if (!report[2]) {
		fprintf(stderr, "grandchild getpid != 2\n");
		return 16;
	}
	if (kid == 1) {
		fprintf(stderr, "parent sees child as pid 1 (namespaces not separated)\n");
		return 17;
	}
	if (!report[3]) {
		fprintf(stderr, "/proc/self or /proc/1 did not resolve inside the namespace\n");
		return 18;
	}

	// unshare(CLONE_NEWPID): the caller stays put, its next child is pid 1 in a
	// new namespace.
	pid_t before = getpid();
	if (unshare(CLONE_NEWPID) == 0) {
		int p2[2];
		if (pipe(p2) != 0) return 19;
		pid_t f = fork();
		if (f == 0) {
			int me = getpid();
			write(p2[1], &me, sizeof me);
			_exit(0);
		}
		int cp = -1;
		read(p2[0], &cp, sizeof cp);
		waitpid(f, 0, 0);
		if (cp != 1) {
			fprintf(stderr, "unshare child getpid=%d want 1\n", cp);
			return 20;
		}
		if (f == cp) {
			fprintf(stderr, "parent sees child as its own namespace pid %d (not separated)\n", f);
			return 21;
		}
		if (getpid() != before) {
			fprintf(stderr, "caller pid changed %d->%d (unshare should not move it)\n", before, getpid());
			return 22;
		}
	}
	return 0;
}
