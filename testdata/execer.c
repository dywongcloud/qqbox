#include <unistd.h>
#include <stdio.h>
int main(void) {
	write(1, "before exec\n", 12);
	char *argv[] = {"busybox", "echo", "exec works", 0};
	char *envp[] = {"PATH=/bin", 0};
	execve("/bin/busybox", argv, envp);
	perror("execve");
	return 3;
}
