#define _GNU_SOURCE
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <sys/syscall.h>

// The cgroup v2 device controller relies on eBPF. laboon fakes it: attach is a
// no-op success, an unsupported command is ENOSYS.
int main(void) {
	char attr[128];
	memset(attr, 0, sizeof attr);
	if (syscall(SYS_bpf, 8 /*BPF_PROG_ATTACH*/, attr, sizeof attr) != 0) { perror("bpf attach"); return 1; }
	if (syscall(SYS_bpf, 0 /*BPF_MAP_CREATE*/, attr, sizeof attr) == 0) { printf("map_create unexpectedly ok\n"); return 2; }
	if (errno != ENOSYS) { printf("map_create errno=%d want ENOSYS\n", errno); return 3; }
	printf("BPFDEV-OK\n");
	return 0;
}
