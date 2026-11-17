// Forks and starts more short-lived children and threads, one at a time, than
// the sandbox has channel slots, so slots must be reused.
#include <pthread.h>
#include <stdio.h>
#include <sys/wait.h>
#include <unistd.h>

static void *nop(void *a) { return a; }

int main(void) {
	for (int i = 0; i < 260; i++) {
		pid_t p = fork();
		if (p < 0) { printf("fork %d failed\n", i); return 1; }
		if (p == 0) _exit(0);
		waitpid(p, 0, 0);
	}
	for (int i = 0; i < 260; i++) {
		pthread_t t;
		if (pthread_create(&t, 0, nop, 0)) { printf("thread %d failed\n", i); return 1; }
		pthread_join(t, 0);
	}
	// Hold 260 children at once: more than there are slots, so a fork fails
	// (EAGAIN) rather than hanging, and the survivors still exit.
	int fds[2], n = 0;
	pipe(fds);
	for (; n < 260; n++) {
		pid_t p = fork();
		if (p < 0) break;
		if (p == 0) { char c; close(fds[1]); read(fds[0], &c, 1); _exit(0); }
	}
	close(fds[1]);
	while (wait(0) > 0) ;
	printf("held %d\n", n);
	printf("churn ok\n");
	return 0;
}
