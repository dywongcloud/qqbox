// vfsmutate.c — the producer half of the overlay round-trip gate: a guest that
// writes a rich change set over a known lower (a new file, a sub-0600-mode file,
// a nested dir+file, a symlink, a lower-file deletion and a lower-file content
// rewrite). The run's kept upper is then serialized to an OCI tar, reloaded and
// materialized, and vfscheck re-runs over the result to prove every change
// survived the serialize/reload round trip.
#include <fcntl.h>
#include <sys/stat.h>
#include <string.h>
#include <unistd.h>

static int put(const char *p, int mode, const char *s) {
	int fd = open(p, O_CREAT | O_TRUNC | O_WRONLY, mode);
	if (fd < 0) return -1;
	int n = (int)strlen(s);
	int w = (int)write(fd, s, n);
	close(fd);
	return w == n ? 0 : -1;
}

int main(void) {
	if (put("/new.txt", 0640, "hello") != 0) return 10;
	if (put("/ro.txt", 0400, "readonly") != 0) return 11; // sub-0600 -> override
	if (mkdir("/sub", 0755) != 0) return 12;
	if (put("/sub/f.txt", 0644, "nested") != 0) return 13;
	if (symlink("/keep.txt", "/sl") != 0) return 14;
	if (unlink("/gone.txt") != 0) return 15;          // lower file -> whiteout
	if (put("/mod.txt", 0644, "changed") != 0) return 16; // lower file -> copy-up
	if (chmod("/keep.txt", 0640) != 0) return 17;     // lower file -> copy-up
	return 0;
}
