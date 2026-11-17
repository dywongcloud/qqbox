// A parent and its forked child both block forever.
#include <stdio.h>
#include <sys/wait.h>
#include <unistd.h>

int main(void) {
	int fds[2];
	char c;
	pipe(fds);
	if (fork() == 0) read(fds[0], &c, 1);
	printf("blocked\n");
	fflush(stdout);
	wait(0);
	return 0;
}
