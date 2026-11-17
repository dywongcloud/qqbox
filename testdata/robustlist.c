#define _GNU_SOURCE
#include <stdio.h>
#include <string.h>
#include <sys/syscall.h>
#include <unistd.h>

// set_robust_list then get_robust_list must round-trip the head and report the
// 24-byte struct size, so a libc robust-mutex support probe (podman's shm lock
// manager does this) sees the syscall as supported rather than ENOSYS.
struct rlh {
	void *next;
	long off;
	void *pending;
};

int main(void) {
	struct rlh h;
	memset(&h, 0, sizeof h);
	if (syscall(SYS_set_robust_list, &h, sizeof h) != 0) { perror("set"); return 1; }
	void *got = (void *)1;
	size_t len = 0;
	if (syscall(SYS_get_robust_list, 0, &got, &len) != 0) { perror("get"); return 2; }
	if (got != &h) { printf("head %p != %p\n", got, (void *)&h); return 3; }
	if (len != sizeof h) { printf("len %zu != %zu\n", len, sizeof h); return 4; }
	printf("ROBUST-OK\n");
	return 0;
}
