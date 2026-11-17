// vfscheck.c — the verifier half of the overlay round-trip gate: a guest that
// runs over the lower produced by serializing vfsmutate's kept upper to an OCI
// tar and materializing it, and asserts every change survived. A distinct exit
// code per failed check pinpoints what the round trip dropped.
#include <fcntl.h>
#include <sys/stat.h>
#include <string.h>
#include <unistd.h>

static int body(const char *p, const char *want) {
	int fd = open(p, O_RDONLY);
	if (fd < 0) return -1;
	char buf[256];
	int n = (int)read(fd, buf, sizeof buf);
	close(fd);
	if (n < 0) return -1;
	return (n == (int)strlen(want) && memcmp(buf, want, n) == 0) ? 0 : -1;
}

int main(void) {
	struct stat st;
	char link[256];

	if (body("/new.txt", "hello") != 0) return 20;
	if (stat("/ro.txt", &st) != 0 || (st.st_mode & 07777) != 0400) return 21;
	if (body("/ro.txt", "readonly") != 0) return 22;
	if (body("/sub/f.txt", "nested") != 0) return 23;
	int n = (int)readlink("/sl", link, sizeof link);
	if (n != 9 || memcmp(link, "/keep.txt", 9) != 0) return 24;
	if (access("/gone.txt", F_OK) == 0) return 25; // whiteout must hide it
	if (body("/mod.txt", "changed") != 0) return 26;
	if (stat("/keep.txt", &st) != 0 || (st.st_mode & 0777) != 0640) return 27;
	return 0;
}
