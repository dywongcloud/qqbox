// copyupmode.c — rewrite the content of a lower file whose mode the host cannot
// carry (0400), without chmod'ing it. The guest runs as root, so it bypasses the
// read-only mode. This copies the file up; its 0400 mode is preserved only in the
// inode side table, never via a chmod — the case the old per-syscall mirror into
// the path map missed and the teardown checkpoint must catch.
#include <fcntl.h>
#include <string.h>
#include <unistd.h>

int main(void) {
	int fd = open("/secret", O_WRONLY | O_TRUNC);
	if (fd < 0) return 10;
	const char *s = "changed";
	if (write(fd, s, strlen(s)) != (long)strlen(s)) return 11;
	close(fd);
	return 0;
}
