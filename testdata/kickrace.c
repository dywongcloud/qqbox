/* A thread sits in futex waits (or, with "read", in reads of stdin, which
   must be a pipe nothing writes); another sends it a signal, waits for its
   handler to run, and sends the next. A signal that lands as the thread goes
   into a wait must still interrupt it: if one is lost, this hangs. Prints
   "kickrace: N signals ok". */
#define _GNU_SOURCE
#include <linux/futex.h>
#include <pthread.h>
#include <semaphore.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/syscall.h>
#include <unistd.h>

static sem_t taken;
static int word, reads;

static void on_usr1(int s)
{
	(void)s;
	sem_post(&taken);
}

static void *waiter(void *arg)
{
	(void)arg;
	char b;
	for (;;) {
		if (reads)
			read(0, &b, 1);
		else
			syscall(SYS_futex, &word, FUTEX_WAIT, 0, NULL, NULL, 0);
	}
	return NULL;
}

int main(int argc, char **argv)
{
	int n = argc > 1 ? atoi(argv[1]) : 20000;
	reads = argc > 2 && argv[2][0] == 'r';
	struct sigaction sa = {.sa_handler = on_usr1};
	sigaction(SIGUSR1, &sa, NULL);
	sem_init(&taken, 0, 0);
	pthread_t t;
	pthread_create(&t, NULL, waiter, NULL);
	for (int i = 0; i < n; i++) {
		pthread_kill(t, SIGUSR1);
		while (sem_wait(&taken) != 0)
			;
	}
	printf("kickrace: %d signals ok\n", n);
	return 0;
}
