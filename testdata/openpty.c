// openpty: a pty pair from /dev/ptmx, the slave opened by its name, and a
// line through it.
#define _GNU_SOURCE
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <termios.h>
#include <unistd.h>

int main(void) {
	int m = posix_openpt(O_RDWR | O_NOCTTY);
	if (m < 0 || grantpt(m) || unlockpt(m)) {
		perror("ptmx");
		return 1;
	}
	char *name = ptsname(m);
	int s = name ? open(name, O_RDWR | O_NOCTTY) : -1;
	if (s < 0) {
		perror(name ? name : "ptsname");
		return 1;
	}
	char buf[16] = {0};
	if (write(m, "hi\n", 3) != 3 || read(s, buf, sizeof buf) != 3 || strcmp(buf, "hi\n") != 0 || !isatty(s)) {
		printf("bad line %s\n", buf);
		return 1;
	}
	printf("pty ok %s\n", strncmp(name, "/dev/pts/", 9) == 0 ? "pts" : name);
	return 0;
}
