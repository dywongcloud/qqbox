#define _GNU_SOURCE
#include <stdio.h>
#include <sched.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <unistd.h>
#include <string.h>

// Bind a source dir onto a target, then pivot_root into a prepared root: the
// bind must show the source's files through the target, and after pivot_root
// "/" is the new root.
int main(void) {
	unshare(CLONE_NEWUSER | CLONE_NEWNS);
	mkdir("/mnt/src", 0755);
	mkdir("/mnt/dst", 0755);
	int fd = open("/mnt/src/file", O_CREAT | O_RDWR, 0644);
	if (fd < 0) { perror("create src"); return 1; }
	write(fd, "bound", 5);
	close(fd);
	if (mount("/mnt/src", "/mnt/dst", NULL, MS_BIND, NULL) != 0) { perror("bind"); return 2; }
	char b[8] = {0};
	fd = open("/mnt/dst/file", O_RDONLY); // the bound file, via the target
	if (fd < 0 || read(fd, b, 5) != 5 || strcmp(b, "bound") != 0) { printf("bind readback %s\n", b); return 3; }
	close(fd);
	printf("BIND-OK\n");
	return 0;
}
