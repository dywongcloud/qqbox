#define _GNU_SOURCE
#include <stdio.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <unistd.h>

// fchmod on a socket and a pipe fd must succeed (operate on the descriptor),
// not try to resolve a filesystem path. A container monitor (conmon) fchmods
// its unbound console-attach socket and its stderr pipe.
int main(void) {
	int s = socket(AF_UNIX, SOCK_SEQPACKET, 0);
	if (s < 0) { perror("socket"); return 1; }
	if (fchmod(s, 0700) != 0) { perror("fchmod socket"); return 2; }
	int p[2];
	if (pipe(p) != 0) { perror("pipe"); return 3; }
	if (fchmod(p[1], 0600) != 0) { perror("fchmod pipe"); return 4; }
	printf("FCHMOD-OK\n");
	return 0;
}
