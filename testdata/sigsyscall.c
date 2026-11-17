/* A thread makes syscalls in a tight loop while another signals it, waiting
   for each handler to run before sending the next. A signal can land anywhere
   on the way into a syscall; after the handler returns, the syscall must still
   return to its own caller with its own result. Prints
   "sigsyscall: N signals ok". */
#define _GNU_SOURCE
#include <pthread.h>
#include <semaphore.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/syscall.h>
#include <unistd.h>

static sem_t taken;
static volatile pid_t self;
static volatile int bad;

static void on_usr1(int s)
{
	(void)s;
	sem_post(&taken);
}

static void *caller(void *arg)
{
	(void)arg;
	for (;;)
		if (getpid() != self)
			bad = 1;
	return NULL;
}

int main(int argc, char **argv)
{
	int n = argc > 1 ? atoi(argv[1]) : 20000;
	self = getpid();
	sem_init(&taken, 0, 0);
	struct sigaction sa = {.sa_handler = on_usr1};
	sigaction(SIGUSR1, &sa, NULL);
	pthread_t t;
	pthread_create(&t, NULL, caller, NULL);
	for (int i = 0; i < n; i++) {
		pthread_kill(t, SIGUSR1);
		while (sem_wait(&taken) != 0)
			;
	}
	if (bad) {
		printf("sigsyscall: a syscall returned a wrong result\n");
		return 1;
	}
	printf("sigsyscall: %d signals ok\n", n);
	return 0;
}
