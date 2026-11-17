// glibc's clone(fn, stack, SIGCHLD, arg): a fork whose child starts on the
// given stack, from which the wrapper takes fn and arg.
#define _GNU_SOURCE
#include <sched.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/wait.h>
#include <unistd.h>

static int child(void *arg) { return *(int *)arg + 1; }

int main(void) {
	size_t sz = 1 << 16;
	char *stk = malloc(sz);
	int v = 41;
	pid_t c = clone(child, stk + sz, SIGCHLD, &v);
	int ws;
	if (c < 0 || waitpid(c, &ws, 0) != c) {
		perror("clone");
		return 1;
	}
	printf("clone child exit %d\n", WEXITSTATUS(ws));
	return 0;
}
