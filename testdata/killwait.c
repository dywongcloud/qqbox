/* A child blocked in each kind of wait the supervisor serves is killed, or
   aborts with a sibling thread blocked there; its parent's waitpid must
   return at once, with the signal: the supervisor learns of the death as an
   event and ends the wait without a timeout. Prints "<case> ok" per case. */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/epoll.h>
#include <sys/file.h>
#include <sys/prctl.h>
#include <sys/ipc.h>
#include <sys/sem.h>
#include <sys/socket.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

static int fails;
static int rfd, lockfd, semid, sock;
static char lockpath[64];

static double now_ms(void)
{
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ts.tv_sec * 1e3 + ts.tv_nsec / 1e6;
}

static void block(const char *how)
{
	char b;
	if (!strcmp(how, "read"))
		read(rfd, &b, 1);
	else if (!strcmp(how, "epoll")) {
		int ep = epoll_create1(0);
		struct epoll_event ev = {.events = EPOLLIN};
		epoll_ctl(ep, EPOLL_CTL_ADD, rfd, &ev);
		epoll_wait(ep, &ev, 1, -1);
	} else if (!strcmp(how, "poll")) {
		struct pollfd p = {.fd = rfd, .events = POLLIN};
		poll(&p, 1, -1);
	} else if (!strcmp(how, "sigsuspend")) {
		sigset_t none;
		sigemptyset(&none);
		sigsuspend(&none);
	} else if (!strcmp(how, "sigtimedwait")) {
		sigset_t s;
		sigemptyset(&s);
		sigaddset(&s, SIGUSR2);
		sigprocmask(SIG_BLOCK, &s, NULL);
		sigwaitinfo(&s, NULL);
	} else if (!strcmp(how, "semop")) {
		struct sembuf op = {.sem_num = 0, .sem_op = -1};
		semop(semid, &op, 1);
	} else if (!strcmp(how, "flock")) {
		int fd = open(lockpath, O_RDWR);
		flock(fd, LOCK_EX);
	} else if (!strcmp(how, "recv")) {
		recv(sock, &b, 1, 0);
	} else if (!strcmp(how, "sleep")) {
		sleep(60);
	}
}

static void *sibling(void *how)
{
	block(how);
	return NULL;
}

static void check(const char *name, pid_t c, int sig)
{
	usleep(100000); /* let it block */
	double t0 = now_ms();
	if (sig == SIGKILL)
		kill(c, SIGKILL);
	int st;
	pid_t w = waitpid(c, &st, 0);
	double ms = now_ms() - t0;
	int ok = w == c && WIFSIGNALED(st) && WTERMSIG(st) == sig && ms < 1000;
	printf("%s %s (%.0f ms)\n", name, ok ? "ok" : "FAIL", ms);
	fflush(stdout);
	if (!ok)
		fails++;
}

int main(void)
{
	const char *hows[] = {"read", "epoll", "poll", "sigsuspend", "sigtimedwait", "semop", "flock", "recv", "sleep"};
	int p[2], sv[2];
	prctl(PR_SET_DUMPABLE, 0); /* the aborts must not stop to dump core */
	if (pipe(p) || socketpair(AF_UNIX, SOCK_STREAM, 0, sv))
		return 2;
	rfd = p[0];
	sock = sv[0];
	semid = semget(IPC_PRIVATE, 1, 0600);
	snprintf(lockpath, sizeof lockpath, "/tmp/killwait.%d", getpid());
	lockfd = open(lockpath, O_RDWR | O_CREAT, 0600);
	flock(lockfd, LOCK_EX);
	for (size_t i = 0; i < sizeof hows / sizeof *hows; i++) {
		char name[64];
		pid_t c = fork();
		if (c == 0) {
			block(hows[i]);
			_exit(0);
		}
		snprintf(name, sizeof name, "kill-%s", hows[i]);
		check(name, c, SIGKILL);
		c = fork();
		if (c == 0) {
			pthread_t t;
			pthread_create(&t, NULL, sibling, (void *)hows[i]);
			usleep(50000);
			abort();
		}
		snprintf(name, sizeof name, "abort-%s", hows[i]);
		check(name, c, SIGABRT);
	}
	semctl(semid, 0, IPC_RMID);
	unlink(lockpath);
	printf("killwait: %d failures\n", fails);
	return fails != 0;
}
