#define _GNU_SOURCE
#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>

// Regression for a hard link within /dev/shm (the devFS mount over the
// overlay): glibc sem_open creates a temp there and link()s it to the final
// name. The link must stay within the one overlay-backed filesystem.
int main(void) {
	int fd = open("/dev/shm/lt_a", O_CREAT | O_EXCL | O_RDWR, 0600);
	if (fd < 0) { perror("open"); return 1; }
	if (write(fd, "x", 1) != 1) { perror("write"); return 2; }
	close(fd);
	if (link("/dev/shm/lt_a", "/dev/shm/lt_b") != 0) { perror("link"); return 3; }
	unlink("/dev/shm/lt_a");
	unlink("/dev/shm/lt_b");
	printf("OK\n");
	return 0;
}
