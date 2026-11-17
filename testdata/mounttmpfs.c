#define _GNU_SOURCE
#include <stdio.h>
#include <sched.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <unistd.h>
#include <string.h>

// mount a tmpfs, write a file into it, read it back: the mount must resolve
// through the vfs like any other path.
int main(void) {
	unshare(CLONE_NEWUSER | CLONE_NEWNS); // gain CAP_SYS_ADMIN in a user ns
	mkdir("/mnt/t", 0755);
	if (mount("none", "/mnt/t", "tmpfs", 0, "") != 0) { perror("mount"); return 1; }
	int fd = open("/mnt/t/f", O_CREAT | O_RDWR, 0644);
	if (fd < 0) { perror("open"); return 2; }
	if (write(fd, "hi", 2) != 2) { perror("write"); return 3; }
	close(fd);
	char b[8] = {0};
	fd = open("/mnt/t/f", O_RDONLY);
	if (fd < 0 || read(fd, b, 2) != 2 || strcmp(b, "hi") != 0) { printf("readback %s\n", b); return 4; }
	close(fd);
	printf("TMPFS-OK\n");
	return 0;
}
