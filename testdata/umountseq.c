#define _GNU_SOURCE
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <sys/mount.h>
#include <errno.h>

// A runtime unmounts in a loop until umount fails. A mounted point unmounts
// once (0), then reports EINVAL — never an endless run of successes.
int main(void) {
	if (mount("none", "/mnt", "tmpfs", 0, NULL) != 0) { perror("mount tmpfs"); return 1; }
	if (umount2("/mnt", MNT_DETACH) != 0) { perror("umount mounted"); return 2; }
	if (umount2("/mnt", MNT_DETACH) == 0) { printf("second umount unexpectedly succeeded\n"); return 3; }
	if (errno != EINVAL) { printf("second umount errno=%d want EINVAL\n", errno); return 4; }
	printf("UMOUNTSEQ-OK\n");
	return 0;
}
