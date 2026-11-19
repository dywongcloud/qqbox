// utsns.c — a child in a new UTS namespace changes its hostname without the
// change leaking to the parent.
#define _GNU_SOURCE
#include <sched.h>
#include <stdio.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

int main(void) {
	char orig[256];
	if (gethostname(orig, sizeof orig) != 0) {
		perror("gethostname");
		return 10;
	}
	pid_t f = fork();
	if (f == 0) {
		if (unshare(CLONE_NEWUTS) != 0) _exit(2);
		if (sethostname("qqbox-uts-child", 16) != 0) _exit(3);
		char h[256] = {0};
		gethostname(h, sizeof h);
		_exit(strcmp(h, "qqbox-uts-child") == 0 ? 0 : 4);
	}
	int st = 0;
	waitpid(f, &st, 0);
	if (!WIFEXITED(st) || WEXITSTATUS(st) != 0) {
		fprintf(stderr, "child status %d\n", st);
		return 11;
	}
	char after[256] = {0};
	gethostname(after, sizeof after);
	if (strcmp(after, orig) != 0) {
		fprintf(stderr, "parent hostname changed to %s\n", after);
		return 12;
	}
	return 0;
}
