#include <unistd.h>
#include <sys/wait.h>
#include <stdio.h>
int main(void) {
	pid_t p = fork();
	if (p == 0) { write(1, "child\n", 6); _exit(7); }
	int st = 0;
	waitpid(p, &st, 0);
	printf("parent saw child pid=%d exit=%d\n", p, WEXITSTATUS(st));
	return 0;
}
