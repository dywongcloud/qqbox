// The main process exits while its children block reading a pipe it holds
// open.
#include <stdlib.h>
#include <unistd.h>

int main(void) {
	int fds[2];
	pipe(fds);
	for (int i = 0; i < 4; i++)
		if (fork() == 0) { char c; read(fds[0], &c, 1); _exit(0); }
	exit(5);
}
