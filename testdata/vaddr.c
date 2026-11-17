// vaddr: the addresses loopback sockets report, as Linux picks them. A
// socket bound to a wildcard is at the address its route picks per
// destination; an accepted socket is at the address its client connected to;
// IPv6's loopback and IPv4's, mapped or not, stay apart; and a connected
// datagram socket takes datagrams from its peer alone.
#include <arpa/inet.h>
#include <errno.h>
#include <netinet/in.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

static struct sockaddr_storage at(const char *ip, int port) {
	struct sockaddr_storage s = {0};
	if (strchr(ip, ':')) {
		struct sockaddr_in6 *a = (void *)&s;
		a->sin6_family = AF_INET6;
		a->sin6_port = htons(port);
		inet_pton(AF_INET6, ip, &a->sin6_addr);
	} else {
		struct sockaddr_in *a = (void *)&s;
		a->sin_family = AF_INET;
		a->sin_port = htons(port);
		inet_pton(AF_INET, ip, &a->sin_addr);
	}
	return s;
}

static socklen_t lenOf(struct sockaddr_storage *s) {
	return s->ss_family == AF_INET6 ? sizeof(struct sockaddr_in6) : sizeof(struct sockaddr_in);
}

// str renders a sockaddr as ip:port.
static const char *str(struct sockaddr_storage *s, char *buf) {
	char ip[INET6_ADDRSTRLEN] = "?";
	int port = 0;
	if (s->ss_family == AF_INET6) {
		struct sockaddr_in6 *a = (void *)s;
		inet_ntop(AF_INET6, &a->sin6_addr, ip, sizeof ip);
		port = ntohs(a->sin6_port);
	} else if (s->ss_family == AF_INET) {
		struct sockaddr_in *a = (void *)s;
		inet_ntop(AF_INET, &a->sin_addr, ip, sizeof ip);
		port = ntohs(a->sin_port);
	}
	snprintf(buf, 80, "%s:%d", ip, port);
	return buf;
}

static int portOf(struct sockaddr_storage *s) {
	return s->ss_family == AF_INET6 ? ntohs(((struct sockaddr_in6 *)s)->sin6_port) : ntohs(((struct sockaddr_in *)s)->sin_port);
}

static const char *local(int fd, char *buf) {
	struct sockaddr_storage s = {0};
	socklen_t l = sizeof s;
	getsockname(fd, (void *)&s, &l);
	return str(&s, buf);
}

static const char *remote(int fd, char *buf) {
	struct sockaddr_storage s = {0};
	socklen_t l = sizeof s;
	getpeername(fd, (void *)&s, &l);
	return str(&s, buf);
}

// sock makes a socket of family fam and type typ bound to ip:port (ip NULL:
// unbound), IPv6's alone when v6only.
static int sock(int fam, int typ, const char *ip, int port, int v6only) {
	int fd = socket(fam, typ, 0), one = 1;
	setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
	if (fam == AF_INET6)
		setsockopt(fd, IPPROTO_IPV6, IPV6_V6ONLY, &v6only, sizeof v6only);
	if (ip) {
		struct sockaddr_storage a = at(ip, port);
		if (bind(fd, (void *)&a, lenOf(&a)) != 0)
			printf("bind %s:%d: %s\n", ip, port, strerror(errno));
	}
	return fd;
}

static int boundPort(int fd) {
	struct sockaddr_storage s = {0};
	socklen_t l = sizeof s;
	getsockname(fd, (void *)&s, &l);
	return portOf(&s);
}

static char want[80];
#define SAME(got, ...) do { const char *g_ = (got); snprintf(want, sizeof want, __VA_ARGS__); CHECK(strcmp(g_, want) == 0, "%s: got %s, want %s", #got, g_, want); } while (0)

// tcp connects a client of family cfam (bound to cip, NULL for none) to
// dst:port, a listener's, and checks both ends' addresses.
static void tcp(int l, int cfam, const char *cip, const char *dst, const char *wantLocal, const char *wantPeerIP) {
	char a[80], b[80];
	int port = boundPort(l);
	int c = sock(cfam, SOCK_STREAM, cip, 0, 0);
	struct sockaddr_storage to = at(dst, port);
	CHECK(connect(c, (void *)&to, lenOf(&to)) == 0, "connect %s:%d: %s", dst, port, strerror(errno));
	int s = accept(l, NULL, NULL);
	CHECK(s >= 0, "accept: %s", strerror(errno));
	SAME(local(s, a), "%s:%d", wantLocal, port);
	snprintf(want, sizeof want, "%s:%d", wantPeerIP, boundPort(c));
	CHECK(strcmp(remote(s, a), want) == 0, "peer of %s->%s: got %s, want %s", cip ? cip : "-", dst, a, want);
	// The client names itself in its own family: an IPv4 one unmapped.
	const char *self = cfam == AF_INET && !strncmp(wantPeerIP, "::ffff:", 7) ? wantPeerIP + 7 : wantPeerIP;
	snprintf(want, sizeof want, "%s:%d", self, boundPort(c));
	CHECK(strcmp(local(c, b), want) == 0, "client of %s->%s at %s, want %s", cip ? cip : "-", dst, b, want);
	close(s);
	close(c);
}

// from receives one datagram on fd and returns its sender as ip:port.
static const char *from(int fd, char *buf) {
	struct sockaddr_storage s = {0};
	socklen_t l = sizeof s;
	char d[8];
	if (recvfrom(fd, d, sizeof d, MSG_DONTWAIT, (void *)&s, &l) < 0)
		snprintf(buf, 80, "none (%s)", strerror(errno));
	else
		str(&s, buf);
	return buf;
}

static void sendTo(int fd, const char *ip, int port, const char *msg) {
	struct sockaddr_storage to = at(ip, port);
	if (sendto(fd, msg, strlen(msg), 0, (void *)&to, lenOf(&to)) < 0)
		printf("sendto %s:%d: %s\n", ip, port, strerror(errno));
}

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	char a[80];

	// A wildcard listener is reached at the address the client named; the
	// client is at the source its route picked.
	int l4 = sock(AF_INET, SOCK_STREAM, "0.0.0.0", 0, 0);
	listen(l4, 8);
	tcp(l4, AF_INET, NULL, "127.0.0.1", "127.0.0.1", "127.0.0.1");
	tcp(l4, AF_INET, NULL, "127.0.0.5", "127.0.0.5", "127.0.0.1");
	tcp(l4, AF_INET, "0.0.0.0", "127.0.0.1", "127.0.0.1", "127.0.0.1");
	close(l4);

	// A dual-stack listener: IPv4 clients arrive mapped, IPv6's loopback as ::1.
	int l6 = sock(AF_INET6, SOCK_STREAM, "::", 0, 0);
	listen(l6, 8);
	tcp(l6, AF_INET, NULL, "127.0.0.1", "::ffff:127.0.0.1", "::ffff:127.0.0.1");
	tcp(l6, AF_INET6, NULL, "::1", "::1", "::1");
	tcp(l6, AF_INET6, "::", "::1", "::1", "::1");
	tcp(l6, AF_INET6, "::", "::ffff:127.0.0.1", "::ffff:127.0.0.1", "::ffff:127.0.0.1");
	close(l6);

	// A datagram from a socket on a wildcard comes from the source its route
	// picks for each destination.
	int r = sock(AF_INET6, SOCK_DGRAM, "::", 0, 0), rp = boundPort(r);
	int w = sock(AF_INET6, SOCK_DGRAM, "::", 0, 0), wp = boundPort(w);
	sendTo(w, "::1", rp, "a");
	SAME(from(r, a), "::1:%d", wp);
	sendTo(w, "::ffff:127.0.0.1", rp, "b");
	SAME(from(r, a), "::ffff:127.0.0.1:%d", wp);
	int w4 = sock(AF_INET, SOCK_DGRAM, NULL, 0, 0);
	sendTo(w4, "127.0.0.1", rp, "c");
	SAME(local(w4, a), "0.0.0.0:%d", boundPort(w4)); // a first send binds the wildcard
	SAME(from(r, a), "::ffff:127.0.0.1:%d", boundPort(w4));

	// Connected, a datagram socket is at its route's source, and takes only
	// its peer's datagrams; those queued before it connected stay.
	int c4 = sock(AF_INET, SOCK_DGRAM, "0.0.0.0", 0, 0), cp = boundPort(c4);
	int p4 = sock(AF_INET, SOCK_DGRAM, "127.0.0.1", 0, 0), pp = boundPort(p4);
	int o4 = sock(AF_INET, SOCK_DGRAM, "127.0.0.1", 0, 0);
	sendTo(o4, "127.0.0.1", cp, "early");
	struct sockaddr_storage peer = at("127.0.0.1", pp);
	CHECK(connect(c4, (void *)&peer, lenOf(&peer)) == 0, "udp connect: %s", strerror(errno));
	SAME(local(c4, a), "127.0.0.1:%d", cp);
	sendTo(o4, "127.0.0.1", cp, "other");
	sendTo(p4, "127.0.0.1", cp, "peer");
	SAME(from(c4, a), "127.0.0.1:%d", boundPort(o4)); // the early one
	SAME(from(c4, a), "127.0.0.1:%d", pp);
	CHECK(strncmp(from(c4, a), "none", 4) == 0, "a non-peer's datagram arrived: %s", a);

	printf("vaddr fails=%d\n", fails);
	return fails != 0;
}
