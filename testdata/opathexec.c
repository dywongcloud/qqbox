#define _GNU_SOURCE
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
extern char **environ;

// Copy self to a temp, O_PATH it, UNLINK the temp, then execveat the O_PATH fd
// (AT_EMPTY_PATH) — crun's re-exec pattern. The pinned descriptor must still be
// executable after its name is gone.
int main(int argc, char **argv) {
	if (argc > 1 && !strcmp(argv[1], "child")) { write(1, "OPATH-EXEC-OK\n", 14); return 0; }
	int self = open("/proc/self/exe", O_RDONLY);
	if (self < 0) { perror("open self"); return 1; }
	int tmp = open("/tmp/oe.bin", O_RDWR | O_CREAT | O_EXCL, 0700);
	if (tmp < 0) { perror("open tmp"); return 2; }
	char buf[65536]; ssize_t n;
	while ((n = read(self, buf, sizeof buf)) > 0) {
		char *p = buf;
		while (n > 0) { ssize_t w = write(tmp, p, n); if (w < 0) { perror("write"); return 3; } p += w; n -= w; }
	}
	close(self); close(tmp);
	int fd = open("/tmp/oe.bin", O_RDONLY | O_PATH);
	if (fd < 0) { perror("O_PATH"); return 4; }
	unlink("/tmp/oe.bin"); // the fd must keep the inode runnable
	char *av[] = {"oe", "child", 0};
	execveat(fd, "", av, environ, AT_EMPTY_PATH);
	perror("execveat");
	return 5;
}
