// SIGTSTP stops a process whose group has a parent in the session to
// continue it, and is discarded in an orphaned group (POSIX).
#include <signal.h>
#include <stdio.h>
#include <sys/wait.h>
#include <unistd.h>

// orphaned: a grandchild in its own group, its parent (the session leader
// of a new session) gone, raises SIGTSTP and goes on.
static int orphaned(void) {
	int p[2];
	if (pipe(p)) return 0;
	pid_t c = fork();
	if (c == 0) {
		setsid();
		pid_t mid = getpid();
		if (fork() == 0) {
			setpgid(0, 0);
			while (getppid() == mid) usleep(10000);
			raise(SIGTSTP);
			write(p[1], "r", 1);
			_exit(0);
		}
		_exit(0);
	}
	close(p[1]);
	waitpid(c, 0, 0);
	char b = 0;
	return read(p[0], &b, 1) == 1 && b == 'r';
}

// stopped: a child in its own group, its parent in the same session, stops.
static int stopped(void) {
	pid_t c = fork();
	if (c == 0) {
		setpgid(0, 0);
		raise(SIGTSTP);
		_exit(0);
	}
	int ws;
	int ok = waitpid(c, &ws, WUNTRACED) == c && WIFSTOPPED(ws) && WSTOPSIG(ws) == SIGTSTP;
	kill(c, SIGCONT);
	waitpid(c, &ws, 0);
	return ok;
}

int main(void) {
	alarm(15); // a stop that should not happen fails the test instead of stalling it
	printf("orphaned=%d stopped=%d\n", orphaned(), stopped());
	return 0;
}
