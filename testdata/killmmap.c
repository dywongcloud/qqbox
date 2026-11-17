// Children killed while mapping files must not leave a wrong file for the
// next mapping to find: after each kill the parent maps two files and checks
// it reads each one's own bytes.
#include <fcntl.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/wait.h>
#include <unistd.h>

#define SZ (256 << 10)

static int mk(const char *path, char c) {
	static char buf[SZ];
	memset(buf, c, sizeof buf);
	int fd = open(path, O_RDWR | O_CREAT | O_TRUNC, 0644);
	return fd >= 0 && write(fd, buf, sizeof buf) == SZ ? fd : -1;
}

static int reads(int fd, char c) {
	char *p = mmap(0, SZ, PROT_READ, MAP_PRIVATE, fd, 0);
	if (p == MAP_FAILED) return 0;
	int ok = p[0] == c && p[SZ - 1] == c;
	munmap(p, SZ);
	return ok;
}

int main(void) {
	alarm(60);
	int a = mk("/tmp/killmmap-a", 'a'), b = mk("/tmp/killmmap-b", 'b');
	if (a < 0 || b < 0) {
		perror("create");
		return 1;
	}
	int bad = 0;
	for (int i = 0; i < 40; i++) {
		pid_t c = fork();
		if (c == 0) {
			for (;;) reads(a, 'a');
		}
		usleep(500 + (i * 137) % 3000);
		kill(c, SIGKILL);
		waitpid(c, 0, 0);
		if (!reads(b, 'b') || !reads(a, 'a')) bad++;
	}
	unlink("/tmp/killmmap-a");
	unlink("/tmp/killmmap-b");
	printf("wrong files %d\n", bad);
	return 0;
}
