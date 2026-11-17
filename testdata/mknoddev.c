#define _GNU_SOURCE
#include <stdio.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/sysmacros.h>

// A container runtime fills the container's /dev with the standard device
// nodes via mknod. Create null and zero, then exercise their device behavior.
int main(void) {
	if (mknod("/tmp/null", S_IFCHR | 0666, makedev(1, 3)) != 0) { perror("mknod null"); return 1; }
	if (mknod("/tmp/zero", S_IFCHR | 0666, makedev(1, 5)) != 0) { perror("mknod zero"); return 2; }
	struct stat st;
	if (stat("/tmp/null", &st) != 0 || !S_ISCHR(st.st_mode) || st.st_rdev != makedev(1, 3)) {
		printf("null stat wrong: mode=%o rdev=%lu\n", st.st_mode, (unsigned long)st.st_rdev); return 3;
	}
	int nul = open("/tmp/null", O_WRONLY);
	if (nul < 0 || write(nul, "discard", 7) != 7) { perror("write null"); return 4; }
	close(nul);
	int z = open("/tmp/zero", O_RDONLY);
	char buf[8] = {1, 1, 1, 1, 1, 1, 1, 1};
	if (z < 0 || read(z, buf, 8) != 8) { perror("read zero"); return 5; }
	for (int i = 0; i < 8; i++) if (buf[i] != 0) { printf("zero not zero at %d\n", i); return 6; }
	close(z);
	printf("MKNODDEV-OK\n");
	return 0;
}
