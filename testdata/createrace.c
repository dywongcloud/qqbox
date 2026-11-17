// createrace: a new file is its creator's from the moment its name exists, as
// on Linux. Two processes of one non-root user race: one makes directories
// (mode 0700), the other creates a file in each as soon as it appears, which
// must never be refused; then both open(O_CREAT) the same new names at once,
// which must always succeed, never fail with EEXIST.
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

#define N 1000

static char base[256];

static void name(char *b, size_t n, const char *kind, int i) { snprintf(b, n, "%s/%s%d", base, kind, i); }

// fill creates a file in each directory the other process makes.
static int fill(void) {
	int fails = 0;
	for (int i = 0; i < N; i++) {
		char d[300], f[320];
		name(d, sizeof d, "d", i);
		snprintf(f, sizeof f, "%s/x", d);
		for (;;) {
			int fd = open(f, O_CREAT | O_WRONLY, 0600);
			if (fd >= 0) {
				close(fd);
				break;
			}
			if (errno != ENOENT) {
				if (fails++ < 5)
					printf("FAIL create in a new directory %s: %s\n", f, strerror(errno));
				break;
			}
		}
	}
	return fails;
}

// both opens each name O_CREAT alongside the other process.
static int both(void) {
	int fails = 0;
	for (int i = 0; i < N; i++) {
		char f[300];
		name(f, sizeof f, "f", i);
		int fd = open(f, O_CREAT | O_RDWR, 0644);
		if (fd < 0) {
			if (fails++ < 5)
				printf("FAIL O_CREAT open of %s: %s\n", f, strerror(errno));
			continue;
		}
		close(fd);
	}
	return fails;
}

int main(int argc, char **argv) {
	snprintf(base, sizeof base, "%s", argc > 1 ? argv[1] : "/tmp/createrace");
	if (mkdir(base, 0777) != 0 && errno != EEXIST) {
		perror("mkdir base");
		return 1;
	}
	chmod(base, 0777);
	if (setgid(4321) != 0 || setuid(4321) != 0) {
		perror("setuid");
		return 1;
	}
	int fails = 0;
	pid_t c = fork();
	if (c == 0)
		_exit(fill() != 0);
	for (int i = 0; i < N; i++) {
		char d[300];
		name(d, sizeof d, "d", i);
		if (mkdir(d, 0700) != 0 && fails++ < 5)
			printf("FAIL mkdir %s: %s\n", d, strerror(errno));
	}
	int st;
	waitpid(c, &st, 0);
	if (!WIFEXITED(st) || WEXITSTATUS(st) != 0)
		fails++;

	c = fork();
	if (c == 0)
		_exit(both() != 0);
	fails += both();
	waitpid(c, &st, 0);
	if (!WIFEXITED(st) || WEXITSTATUS(st) != 0)
		fails++;

	printf("createrace fails=%d\n", fails);
	return fails != 0;
}
