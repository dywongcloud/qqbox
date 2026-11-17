// SIGKILL ends a child blocked reading an empty pipe or writing a full one,
// and its parent reaps it at once.
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

static int blocked(int reader) {
	int p[2];
	if (pipe(p)) return 0;
	pid_t c = fork();
	if (c == 0) {
		char buf[65536];
		memset(buf, 'x', sizeof buf);
		if (reader) {
			read(p[0], buf, 1);
		} else {
			for (;;) write(p[1], buf, sizeof buf);
		}
		_exit(0);
	}
	usleep(200000);
	kill(c, SIGKILL);
	int ws;
	return waitpid(c, &ws, 0) == c && WIFSIGNALED(ws) && WTERMSIG(ws) == SIGKILL;
}

int main(void) {
	alarm(15); // a hang fails the test instead of stalling it
	printf("reader=%d writer=%d\n", blocked(1), blocked(0));
	return 0;
}
