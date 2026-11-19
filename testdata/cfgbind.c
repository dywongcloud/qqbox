// cfgbind.c: read a file from a host-directory bind mount and write one back,
// to prove Config.Mounts exposes the host subtree read-write at the guest path.
#include <unistd.h>
#include <fcntl.h>
int main(void) {
	char b[64];
	int fd = open("/qqboxbind/hello", O_RDONLY);
	if (fd < 0) return 1;
	int n = read(fd, b, sizeof b);
	close(fd);
	if (n <= 0) return 2;
	int w = open("/qqboxbind/out", O_WRONLY | O_CREAT | O_TRUNC, 0644);
	if (w < 0) return 3;
	if (write(w, b, n) != n) return 4;
	close(w);
	write(1, "BIND-FILE-OK", 12);
	return 0;
}
