#define _GNU_SOURCE
#include <stdio.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>

// A container runtime creates the container's cgroup and configures it through
// control files. Exercise mkdir, a recorded-and-read-back write, nesting, and
// rmdir on laboon's virtual cgroup v2 hierarchy.
static int wr(const char *p, const char *v) {
	int fd = open(p, O_WRONLY);
	if (fd < 0) return -1;
	int n = write(fd, v, strlen(v));
	close(fd);
	return n < 0 ? -1 : 0;
}
static int rd(const char *p, char *buf, int n) {
	int fd = open(p, O_RDONLY);
	if (fd < 0) return -1;
	int r = read(fd, buf, n - 1);
	close(fd);
	if (r < 0) return -1;
	buf[r] = 0;
	return 0;
}
int main(void) {
	char buf[256];
	if (rd("/sys/fs/cgroup/cgroup.controllers", buf, sizeof buf) || !strstr(buf, "memory")) {
		printf("controllers=%s\n", buf); return 1;
	}
	if (mkdir("/sys/fs/cgroup/lab", 0755) != 0) { perror("mkdir lab"); return 2; }
	if (wr("/sys/fs/cgroup/lab/cgroup.procs", "4321") != 0) { perror("write procs"); return 3; }
	if (rd("/sys/fs/cgroup/lab/cgroup.procs", buf, sizeof buf) || strcmp(buf, "4321") != 0) {
		printf("procs readback=%s\n", buf); return 4;
	}
	if (mkdir("/sys/fs/cgroup/lab/ctr", 0755) != 0) { perror("mkdir nested"); return 5; }
	if (rmdir("/sys/fs/cgroup/lab") == 0) { printf("rmdir non-empty should fail\n"); return 6; }
	if (rmdir("/sys/fs/cgroup/lab/ctr") != 0) { perror("rmdir ctr"); return 7; }
	if (rmdir("/sys/fs/cgroup/lab") != 0) { perror("rmdir lab"); return 8; }
	if (access("/sys/fs/cgroup/lab", F_OK) == 0) { printf("lab still exists\n"); return 9; }
	printf("CGROUPFS-OK\n");
	return 0;
}
