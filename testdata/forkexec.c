#include <unistd.h>
#include <sys/wait.h>
#include <stdio.h>
int main(void) {
	pid_t p = fork();
	if (p == 0) { char *a[]={"busybox","echo","hello-from-exec",0},*e[]={0}; execve("/bin/busybox",a,e); _exit(99); }
	int st=0; waitpid(p,&st,0);
	printf("done, child exit=%d\n", WEXITSTATUS(st));
	return 0;
}
