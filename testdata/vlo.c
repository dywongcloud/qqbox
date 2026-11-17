// vlo: a run with networking has its own loopback, as a network namespace
// has. A listener on a wildcard takes connections both from the run's own
// loopback and from the network (the host connecting to its port), on one
// port; a datagram socket on a wildcard receives from the run's loopback; and
// a service on the host's 127.0.0.1 is out of reach.
// Usage: vlo <port> <hostport>: listen on port, which the host connects to
// once "ready" is printed; hostport is a host service's on 127.0.0.1.
#include <arpa/inet.h>
#include <errno.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/wait.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

static struct sockaddr_in at(const char *ip, int port) {
	struct sockaddr_in a = {.sin_family = AF_INET, .sin_port = htons(port)};
	inet_pton(AF_INET, ip, &a.sin_addr);
	return a;
}

// takes accepts one connection on l and checks it carries msg, from a peer
// on ip (NULL: any).
static void takes(int l, const char *msg, const char *ip) {
	struct sockaddr_in peer;
	socklen_t pl = sizeof peer;
	int a = accept(l, (struct sockaddr *)&peer, &pl);
	CHECK(a >= 0, "accept: %s", strerror(errno));
	if (a < 0)
		return;
	char buf[16] = {0};
	ssize_t n = read(a, buf, sizeof buf - 1);
	CHECK(n == (ssize_t)strlen(msg) && strcmp(buf, msg) == 0, "read %zd \"%s\", want %s", n, buf, msg);
	char from[INET_ADDRSTRLEN];
	inet_ntop(AF_INET, &peer.sin_addr, from, sizeof from);
	CHECK(ip == NULL || strcmp(from, ip) == 0, "%s came from %s, not %s", msg, from, ip);
	close(a);
}

int main(int argc, char **argv) {
	if (argc < 3)
		return 2;
	setvbuf(stdout, NULL, _IONBF, 0);
	int port = atoi(argv[1]);

	int l = socket(AF_INET, SOCK_STREAM, 0), one = 1;
	setsockopt(l, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
	struct sockaddr_in any = at("0.0.0.0", port);
	CHECK(bind(l, (struct sockaddr *)&any, sizeof any) == 0, "bind 0.0.0.0:%d: %s", port, strerror(errno));
	CHECK(listen(l, 4) == 0, "listen: %s", strerror(errno));

	// From the run's own loopback.
	pid_t pid = fork();
	if (pid == 0) {
		int c = socket(AF_INET, SOCK_STREAM, 0);
		struct sockaddr_in to = at("127.0.0.1", port);
		if (connect(c, (struct sockaddr *)&to, sizeof to) != 0 || write(c, "lo", 2) != 2)
			_exit(1);
		_exit(0);
	}
	takes(l, "lo", "127.0.0.1");
	int st = 0;
	waitpid(pid, &st, 0);
	CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0, "the loopback client failed: %#x", st);

	// From the network: the host connects to the port.
	printf("ready\n");
	takes(l, "host", NULL);
	close(l);

	// A datagram socket on a wildcard receives from the run's own loopback.
	int u = socket(AF_INET, SOCK_DGRAM, 0), s = socket(AF_INET, SOCK_DGRAM, 0);
	struct sockaddr_in ua = at("0.0.0.0", 0);
	socklen_t ul = sizeof ua;
	CHECK(bind(u, (struct sockaddr *)&ua, sizeof ua) == 0 && getsockname(u, (struct sockaddr *)&ua, &ul) == 0, "udp bind: %s", strerror(errno));
	struct sockaddr_in to = at("127.0.0.1", ntohs(ua.sin_port));
	CHECK(sendto(s, "u", 1, 0, (struct sockaddr *)&to, sizeof to) == 1, "sendto: %s", strerror(errno));
	char buf[4] = {0};
	struct sockaddr_in from;
	socklen_t fl = sizeof from;
	ssize_t n = recvfrom(u, buf, sizeof buf, 0, (struct sockaddr *)&from, &fl);
	char fip[INET_ADDRSTRLEN];
	inet_ntop(AF_INET, &from.sin_addr, fip, sizeof fip);
	CHECK(n == 1 && buf[0] == 'u' && strcmp(fip, "127.0.0.1") == 0, "recvfrom %zd from %s", n, fip);

	// The host's loopback is not the run's.
	int c = socket(AF_INET, SOCK_STREAM, 0);
	struct sockaddr_in host = at("127.0.0.1", atoi(argv[2]));
	int r = connect(c, (struct sockaddr *)&host, sizeof host);
	CHECK(r == -1 && errno == ECONNREFUSED, "connect to the host's 127.0.0.1:%s: %d %s", argv[2], r, strerror(errno));

	printf("vlo fails=%d\n", fails);
	return fails != 0;
}
