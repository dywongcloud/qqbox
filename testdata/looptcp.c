#define _GNU_SOURCE
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <sched.h>
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <sys/wait.h>
// In a fresh netns, a TCP server and client on 127.0.0.1 must talk over vnet.
int main(void) {
	if (unshare(CLONE_NEWNET) != 0) perror("unshare(non-fatal)");
	int ls = socket(AF_INET, SOCK_STREAM, 0);
	if (ls < 0) { perror("socket"); return 1; }
	struct sockaddr_in a; memset(&a,0,sizeof a);
	a.sin_family=AF_INET; a.sin_addr.s_addr=htonl(INADDR_LOOPBACK); a.sin_port=htons(54321);
	if (bind(ls,(void*)&a,sizeof a)!=0){perror("bind");return 2;}
	if (listen(ls,8)!=0){perror("listen");return 3;}
	pid_t pid=fork();
	if (pid==0){ // client
		int cs=socket(AF_INET,SOCK_STREAM,0);
		struct sockaddr_in d; memset(&d,0,sizeof d);
		d.sin_family=AF_INET; d.sin_addr.s_addr=htonl(INADDR_LOOPBACK); d.sin_port=htons(54321);
		if (connect(cs,(void*)&d,sizeof d)!=0){perror("connect");_exit(21);}
		if (write(cs,"ping",4)!=4){perror("write");_exit(22);}
		char r[8]={0}; int n=read(cs,r,sizeof r);
		if (n!=4||memcmp(r,"pong",4)!=0){fprintf(stderr,"client got %d %.*s\n",n,n,r);_exit(23);}
		_exit(0);
	}
	int cfd=accept(ls,NULL,NULL);
	if (cfd<0){perror("accept");return 4;}
	char b[8]={0}; int n=read(cfd,b,sizeof b);
	if (n!=4||memcmp(b,"ping",4)!=0){fprintf(stderr,"server got %d %.*s\n",n,n,b);return 5;}
	if (write(cfd,"pong",4)!=4){perror("write pong");return 6;}
	int st=0; waitpid(pid,&st,0);
	if (!WIFEXITED(st)||WEXITSTATUS(st)!=0){fprintf(stderr,"client exit %d\n",st);return 7;}
	printf("LOOPTCP-OK\n");
	return 0;
}
