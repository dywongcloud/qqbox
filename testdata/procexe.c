#define _GNU_SOURCE
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
extern char **environ;

// Re-exec self from an O_PATH fd of an unlinked copy, then (in the child)
// confirm /proc/self/exe still opens and reads back with the " (deleted)"
// suffix Linux shows — crun relies on re-reading its exe after the re-exec.
int main(int argc, char **argv) {
	if (argc > 1 && !strcmp(argv[1], "child")) {
		int fd = open("/proc/self/exe", O_RDONLY);
		if (fd < 0) { perror("open /proc/self/exe"); return 10; }
		close(fd);
		char b[256] = {0};
		ssize_t n = readlink("/proc/self/exe", b, sizeof b - 1);
		if (n <= 0 || !strstr(b, "(deleted)")) { printf("readlink=%s\n", b); return 11; }
		write(1, "PROCEXE-OK\n", 11);
		return 0;
	}
	int self = open("/proc/self/exe", O_RDONLY);
	int tmp = open("/tmp/pe.bin", O_RDWR | O_CREAT | O_EXCL, 0700);
	if (self < 0 || tmp < 0) { perror("open"); return 1; }
	char buf[65536]; ssize_t n;
	while ((n = read(self, buf, sizeof buf)) > 0) write(tmp, buf, n);
	close(self); close(tmp);
	int fd = open("/tmp/pe.bin", O_RDONLY | O_PATH);
	unlink("/tmp/pe.bin");
	char *av[] = {"pe", "child", 0};
	execveat(fd, "", av, environ, AT_EMPTY_PATH);
	perror("execveat");
	return 2;
}
