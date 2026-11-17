#define _GNU_SOURCE
#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>

// A container runtime fchowns the std streams it points at /dev/null and pipes.
// Those must succeed (as root on Linux), not fail resolving a path.
int main(void) {
	if (fchown(1, 0, 0) != 0) { perror("fchown pipe stdout"); /* stdout may be a pipe */ }
	int nul = open("/dev/null", O_RDWR);
	if (nul < 0) { perror("open /dev/null"); return 1; }
	if (fchown(nul, 0, 0) != 0) { perror("fchown /dev/null"); return 2; }
	int p[2];
	if (pipe(p) != 0) { perror("pipe"); return 3; }
	if (fchown(p[1], 0, 0) != 0) { perror("fchown pipe"); return 4; }
	printf("FCHOWN-OK\n");
	return 0;
}
