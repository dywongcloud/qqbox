#define _GNU_SOURCE
#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/mount.h>
#include <sys/sysmacros.h>
// mknod a char device inside a tmpfs mount; fstat must report rdev=1:3.
int main(void) {
	mkdir("/t", 0755);
	if (mount("none", "/t", "tmpfs", 0, NULL) != 0) { perror("mount tmpfs"); return 1; }
	if (mknod("/t/null", S_IFCHR | 0666, makedev(1, 3)) != 0) { perror("mknod"); return 2; }
	struct stat ls;
	if (stat("/t/null", &ls) != 0) { perror("stat"); return 3; }
	printf("path-stat: ischr=%d rdev=%lu:%lu\n", S_ISCHR(ls.st_mode), (unsigned long)major(ls.st_rdev), (unsigned long)minor(ls.st_rdev));
	int fd = open("/t/null", O_RDWR);
	if (fd < 0) { perror("open"); return 4; }
	struct stat fs;
	if (fstat(fd, &fs) != 0) { perror("fstat"); return 5; }
	printf("fstat: ischr=%d rdev=%lu:%lu\n", S_ISCHR(fs.st_mode), (unsigned long)major(fs.st_rdev), (unsigned long)minor(fs.st_rdev));
	return 0;
}
