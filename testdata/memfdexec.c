#define _GNU_SOURCE
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

extern char **environ;

// Copy this binary into a memfd and exec it via /proc/self/fd/<n>, as a
// container runtime does to run a sealed copy of itself. The re-exec runs the
// "child" branch and prints the sentinel.
int main(int argc, char **argv) {
	if (argc > 1 && strcmp(argv[1], "child") == 0) {
		write(1, "MEMFD-EXEC-OK\n", 14);
		return 0;
	}
	int self = open("/proc/self/exe", O_RDONLY);
	if (self < 0) { perror("open self"); return 1; }
	int mfd = memfd_create("t", MFD_CLOEXEC);
	if (mfd < 0) { perror("memfd"); return 2; }
	char buf[65536];
	ssize_t n;
	while ((n = read(self, buf, sizeof buf)) > 0) {
		char *p = buf;
		while (n > 0) {
			ssize_t w = write(mfd, p, n);
			if (w < 0) { perror("write"); return 3; }
			p += w; n -= w;
		}
	}
	close(self);
	char path[64];
	snprintf(path, sizeof path, "/proc/self/fd/%d", mfd);
	char *av[] = {path, "child", 0};
	execve(path, av, environ);
	perror("execve");
	return 4;
}
