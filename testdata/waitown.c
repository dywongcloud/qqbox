// A parent's wait must not reap its grandchild: the child waits for that.
#include <stdio.h>
#include <sys/wait.h>
#include <unistd.h>

int main(void) {
	pid_t a = fork();
	if (a == 0) {
		pid_t b = fork();
		if (b == 0) _exit(9);
		for (volatile long i = 0; i < 50000000; i++) ;
		int st = 0;
		if (waitpid(b, &st, 0) != b || WEXITSTATUS(st) != 9) _exit(1);
		_exit(4);
	}
	int st = 0;
	pid_t got = wait(&st);
	printf("waited %s exit=%d\n", got == a ? "child" : "other", WEXITSTATUS(st));
	return 0;
}
