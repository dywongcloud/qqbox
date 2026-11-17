#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/wait.h>
#include <stdio.h>
static size_t eat(int cap){size_t t=0;for(int i=0;i<cap;i++){void*p=malloc(1<<20);if(!p)break;memset(p,1,1<<20);t++;}return t;}
int main(void){
	pid_t p=fork();
	if(p==0){ size_t c=eat(1000); printf("child got %zu MiB\n",c); _exit(0); }
	int st; waitpid(p,&st,0);
	size_t pp=eat(1000); printf("parent got %zu MiB\n",pp);
	return 0;
}
