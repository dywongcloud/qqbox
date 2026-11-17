// vreuse: sockets sharing a loopback port, as Linux lets them. Datagram
// sockets share by SO_REUSEADDR on both or SO_REUSEPORT on both: a datagram
// goes to the newest of the sharers bound to the address it names, to one
// connected to its sender before any other, and, by SO_REUSEPORT, spread by
// flow. Stream sockets may bind together by SO_REUSEADDR while none listens,
// and listen together only by SO_REUSEPORT.
#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

static int lastErr;

// sock makes a socket of type typ bound to ip:port with SO_REUSEADDR ra and
// SO_REUSEPORT rp; -1 (lastErr the errno) when the bind fails.
static int sock(int typ, const char *ip, int port, int ra, int rp) {
	int fd = socket(AF_INET, typ, 0);
	setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &ra, sizeof ra);
	setsockopt(fd, SOL_SOCKET, SO_REUSEPORT, &rp, sizeof rp);
	struct sockaddr_in a = {.sin_family = AF_INET, .sin_port = htons(port)};
	inet_pton(AF_INET, ip, &a.sin_addr);
	if (bind(fd, (void *)&a, sizeof a) != 0) {
		lastErr = errno;
		close(fd);
		return -1;
	}
	return fd;
}

static int portOf(int fd) {
	struct sockaddr_in a = {0};
	socklen_t l = sizeof a;
	getsockname(fd, (void *)&a, &l);
	return ntohs(a.sin_port);
}

static void sendTo(int fd, int port) {
	struct sockaddr_in a = {.sin_family = AF_INET, .sin_port = htons(port)};
	inet_pton(AF_INET, "127.0.0.1", &a.sin_addr);
	sendto(fd, "x", 1, 0, (void *)&a, sizeof a);
}

static int got(int fd) {
	char b[4];
	return fd >= 0 && recv(fd, b, sizeof b, MSG_DONTWAIT) > 0;
}

// freePort is a port no socket holds now.
static int freePort(int typ) {
	int fd = sock(typ, "127.0.0.1", 0, 0, 0), p = portOf(fd);
	close(fd);
	return p;
}

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	int s = sock(SOCK_DGRAM, "127.0.0.1", 0, 0, 0);

	int p = freePort(SOCK_DGRAM);
	int a = sock(SOCK_DGRAM, "127.0.0.1", p, 1, 0), b = sock(SOCK_DGRAM, "127.0.0.1", p, 1, 0);
	CHECK(a >= 0 && b >= 0, "two SO_REUSEADDR datagram sockets on one address: %s", strerror(lastErr));
	sendTo(s, p);
	CHECK(!got(a) && got(b), "the newest sharer takes the datagram");

	p = freePort(SOCK_DGRAM);
	int w = sock(SOCK_DGRAM, "0.0.0.0", p, 1, 0), sp = sock(SOCK_DGRAM, "127.0.0.1", p, 1, 0);
	sendTo(s, p);
	CHECK(!got(w) && got(sp), "the one bound to the address takes it, not the wildcard");

	p = freePort(SOCK_DGRAM);
	int c = sock(SOCK_DGRAM, "127.0.0.1", p, 0, 0), d = sock(SOCK_DGRAM, "127.0.0.1", p, 1, 0);
	CHECK(c >= 0 && d < 0 && lastErr == EADDRINUSE, "a sharer over one without SO_REUSEADDR: %d %s", d, strerror(lastErr));

	p = freePort(SOCK_DGRAM);
	int e = sock(SOCK_DGRAM, "127.0.0.1", p, 1, 0), f = sock(SOCK_DGRAM, "127.0.0.1", p, 0, 1);
	CHECK(e >= 0 && f < 0 && lastErr == EADDRINUSE, "SO_REUSEPORT over SO_REUSEADDR: %d %s", f, strerror(lastErr));

	p = freePort(SOCK_DGRAM);
	int r1 = sock(SOCK_DGRAM, "127.0.0.1", p, 0, 1), r2 = sock(SOCK_DGRAM, "127.0.0.1", p, 0, 1);
	int n1 = 0, n2 = 0;
	for (int i = 0; i < 20; i++) {
		int t = sock(SOCK_DGRAM, "127.0.0.1", 0, 0, 0);
		sendTo(t, p);
		n1 += got(r1);
		n2 += got(r2);
		close(t);
	}
	CHECK(r2 >= 0 && n1 + n2 == 20 && n1 > 0 && n2 > 0, "SO_REUSEPORT spreads flows: %d and %d", n1, n2);

	p = freePort(SOCK_DGRAM);
	int g = sock(SOCK_DGRAM, "127.0.0.1", p, 1, 0), h = sock(SOCK_DGRAM, "127.0.0.1", p, 1, 0);
	struct sockaddr_in sa = {0};
	socklen_t sl = sizeof sa;
	getsockname(s, (void *)&sa, &sl);
	connect(g, (void *)&sa, sizeof sa);
	sendTo(s, p);
	CHECK(got(g) && !got(h), "the sharer connected to the sender takes it");

	p = freePort(SOCK_STREAM);
	int t1 = sock(SOCK_STREAM, "127.0.0.1", p, 1, 0), t2 = sock(SOCK_STREAM, "127.0.0.1", p, 1, 0);
	CHECK(t1 >= 0 && t2 >= 0, "two SO_REUSEADDR stream sockets bind before either listens: %s", strerror(lastErr));
	CHECK(listen(t1, 4) == 0, "the first listens: %s", strerror(errno));
	errno = 0;
	CHECK(t2 >= 0 && listen(t2, 4) == -1 && errno == EADDRINUSE, "the second listens too: %s", strerror(errno));
	int t3 = sock(SOCK_STREAM, "127.0.0.1", p, 1, 0);
	CHECK(t3 < 0 && lastErr == EADDRINUSE, "a SO_REUSEADDR bind over a listener: %d %s", t3, strerror(lastErr));

	p = freePort(SOCK_STREAM);
	int l1 = sock(SOCK_STREAM, "127.0.0.1", p, 0, 1), l2 = sock(SOCK_STREAM, "127.0.0.1", p, 0, 1);
	CHECK(l1 >= 0 && l2 >= 0 && listen(l1, 16) == 0 && listen(l2, 16) == 0, "two SO_REUSEPORT listeners: %s", strerror(errno));
	fcntl(l1, F_SETFL, O_NONBLOCK);
	fcntl(l2, F_SETFL, O_NONBLOCK);
	int a1 = 0, a2 = 0;
	for (int i = 0; i < 20; i++) {
		int cl = socket(AF_INET, SOCK_STREAM, 0);
		struct sockaddr_in to = {.sin_family = AF_INET, .sin_port = htons(p)};
		inet_pton(AF_INET, "127.0.0.1", &to.sin_addr);
		if (connect(cl, (void *)&to, sizeof to) == 0) {
			int x;
			if ((x = accept(l1, NULL, NULL)) >= 0) {
				a1++;
				close(x);
			} else if ((x = accept(l2, NULL, NULL)) >= 0) {
				a2++;
				close(x);
			}
		}
		close(cl);
	}
	CHECK(a1 + a2 == 20 && a1 > 0 && a2 > 0, "SO_REUSEPORT spreads connections: %d and %d", a1, a2);
	int l3 = sock(SOCK_STREAM, "127.0.0.1", p, 0, 0);
	CHECK(l3 < 0 && lastErr == EADDRINUSE, "a plain bind over SO_REUSEPORT listeners: %d %s", l3, strerror(lastErr));

	printf("vreuse fails=%d\n", fails);
	return fails != 0;
}
