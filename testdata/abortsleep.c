// A child whose other thread sleeps aborts: its process ends at once, so the
// parent sees the pipe it held close and its SIGABRT status, well before the
// sleep would end.
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

static void *sleeper(void *arg) {
	(void)arg;
	sleep(30);
	return NULL;
}

int main(void) {
	int p[2];
	if (pipe(p)) return 1;
	struct timespec t0, t1;
	clock_gettime(CLOCK_MONOTONIC, &t0);
	pid_t c = fork();
	if (c == 0) {
		close(p[0]);
		pthread_t t;
		pthread_create(&t, NULL, sleeper, NULL);
		usleep(100000);
		abort();
	}
	close(p[1]);
	alarm(15); // a hang fails the test instead of stalling it
	char b;
	ssize_t n = read(p[0], &b, 1);
	int ws;
	waitpid(c, &ws, 0);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	double s = (t1.tv_sec - t0.tv_sec) + (t1.tv_nsec - t0.tv_nsec) / 1e9;
	printf("eof=%d sigabrt=%d fast=%d\n", n == 0, WIFSIGNALED(ws) && WTERMSIG(ws) == SIGABRT, s < 10);
	return 0;
}
