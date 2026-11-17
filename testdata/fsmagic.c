#define _GNU_SOURCE
#include <stdio.h>
#include <sys/vfs.h>

// The pseudo mounts must report their real statfs magic (a container runtime
// checks /sys/fs/cgroup is cgroup2), not the overlay's.
int main(void) {
	struct statfs s;
	if (statfs("/sys/fs/cgroup", &s) != 0) { perror("statfs cgroup"); return 1; }
	if ((unsigned long)s.f_type != 0x63677270UL) { printf("cgroup f_type=%#lx\n", (unsigned long)s.f_type); return 2; }
	if (statfs("/proc", &s) != 0) { perror("statfs proc"); return 3; }
	if ((unsigned long)s.f_type != 0x9fa0UL) { printf("proc f_type=%#lx\n", (unsigned long)s.f_type); return 4; }
	printf("FSMAGIC-OK\n");
	return 0;
}
