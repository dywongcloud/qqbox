#define _GNU_SOURCE
#include <fcntl.h>
#include <sched.h>
#include <stdio.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <unistd.h>

// mount -t overlay, as a container runtime's overlay storage driver issues it:
// union a lower layer and a writable upper, confirm a lower file shows in the
// merged view and a write into merged copies up into the upper layer.
static int writef(const char *p, const char *s) {
	int fd = open(p, O_CREAT | O_WRONLY | O_TRUNC, 0644);
	if (fd < 0) return -1;
	int n = strlen(s), r = write(fd, s, n);
	close(fd);
	return r == n ? 0 : -1;
}

static int readeq(const char *p, const char *want) {
	char b[32] = {0};
	int fd = open(p, O_RDONLY);
	if (fd < 0) return -1;
	int r = read(fd, b, sizeof b - 1);
	close(fd);
	return (r > 0 && strcmp(b, want) == 0) ? 0 : -1;
}

int main(void) {
	if (unshare(CLONE_NEWUSER | CLONE_NEWNS) != 0) { perror("unshare"); return 1; } // CAP_SYS_ADMIN
	char base[64];
	snprintf(base, sizeof base, "/tmp/lcov-%d", (int)getpid());
	char lower[96], upper[96], work[96], merged[96], p[160], data[400];
	snprintf(lower, sizeof lower, "%s/lower", base);
	snprintf(upper, sizeof upper, "%s/upper", base);
	snprintf(work, sizeof work, "%s/work", base);
	snprintf(merged, sizeof merged, "%s/merged", base);
	mkdir(base, 0755);
	mkdir(lower, 0755);
	mkdir(upper, 0755);
	mkdir(work, 0755);
	mkdir(merged, 0755);

	snprintf(p, sizeof p, "%s/a", lower);
	if (writef(p, "LOWER") != 0) { perror("write lower/a"); return 2; }

	snprintf(data, sizeof data, "lowerdir=%s,upperdir=%s,workdir=%s", lower, upper, work);
	if (mount("overlay", merged, "overlay", 0, data) != 0) { perror("mount overlay"); return 3; }

	snprintf(p, sizeof p, "%s/a", merged); // the lower file shows in the merged view
	if (readeq(p, "LOWER") != 0) { fprintf(stderr, "lower file not merged\n"); return 4; }

	snprintf(p, sizeof p, "%s/newf", merged); // a write into merged copies up
	if (writef(p, "UP") != 0) { perror("write merged/newf"); return 5; }
	snprintf(p, sizeof p, "%s/newf", upper);
	if (readeq(p, "UP") != 0) { fprintf(stderr, "write did not land in the upper layer\n"); return 6; }

	printf("OVERLAY-OK\n");
	return 0;
}
