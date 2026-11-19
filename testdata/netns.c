// netns.c — with networking enabled, a process in the host network namespace
// can use the host network. Once it enters a new (empty) network namespace it
// can still open an AF_INET socket (vnet owns the namespace), but cannot reach
// the host network: a connect to a non-loopback address is unreachable.
// AF_UNIX works for local IPC and its abstract names are per-namespace.
#define _GNU_SOURCE
#include <errno.h>
#include <sched.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

static socklen_t abstractAddr(struct sockaddr_un *a, const char *name) {
	memset(a, 0, sizeof *a);
	a->sun_family = AF_UNIX;
	size_t n = strlen(name);
	memcpy(a->sun_path + 1, name, n);
	return (socklen_t)(offsetof(struct sockaddr_un, sun_path) + 1 + n);
}

int main(void) {
	int s = socket(AF_INET, SOCK_STREAM, 0);
	if (s < 0) { perror("host inet socket"); return 10; }
	close(s);

	struct sockaddr_un a;
	socklen_t al = abstractAddr(&a, "qqbox-netns-probe");
	int hs = socket(AF_UNIX, SOCK_STREAM, 0);
	if (hs < 0 || bind(hs, (struct sockaddr *)&a, al) != 0) { perror("bind host abstract"); return 15; }

	if (unshare(CLONE_NEWNET) != 0) { perror("unshare(CLONE_NEWNET)"); return 11; }

	// In the empty netns an INET socket can be created (vnet owns it)...
	int s2 = socket(AF_INET, SOCK_STREAM, 0);
	if (s2 < 0) { fprintf(stderr, "inet socket refused in netns: errno=%d\n", errno); return 12; }
	// ...but it cannot reach the host network.
	struct sockaddr_in sin;
	memset(&sin, 0, sizeof sin);
	sin.sin_family = AF_INET;
	sin.sin_port = htons(53);
	inet_pton(AF_INET, "8.8.8.8", &sin.sin_addr);
	errno = 0;
	if (connect(s2, (struct sockaddr *)&sin, sizeof sin) == 0) {
		fprintf(stderr, "reached the host network from an isolated netns\n");
		return 13;
	}
	if (errno != ENETUNREACH && errno != ENETDOWN && errno != EHOSTUNREACH) {
		fprintf(stderr, "connect errno=%d, want unreachable\n", errno);
		return 17;
	}
	close(s2);

	int u = socket(AF_UNIX, SOCK_STREAM, 0);
	if (u < 0) { perror("AF_UNIX socket in netns"); return 14; }
	if (bind(u, (struct sockaddr *)&a, al) != 0) { perror("bind abstract in new netns"); return 16; }
	close(u);
	close(hs);
	return 0;
}
