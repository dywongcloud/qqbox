// vmcast: IPv4 multicast in a network namespace whose only interface is the
// loopback, as Linux does it: no route to a group (ENODEV, ENETUNREACH), a
// group joined on lo by address, a datagram sent out of lo reaching every
// socket bound to its port while lo has the group (unless IP_MULTICAST_ALL is
// off for one that did not join it), whatever IP_MULTICAST_LOOP says, as the
// loopback carries it back; leaving; a group-bound socket sending from the
// route's address; and IPv6, which lo carries no multicast route for: joins,
// sends failing whatever interface is named, and a group bound to with an
// interface that exists when its scope needs one.
#include <arpa/inet.h>
#include <errno.h>
#include <net/if.h>
#include <netinet/in.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

// err runs a call and yields 0 or the errno it failed with.
#define ERR(call) (errno = 0, (call) < 0 ? errno : 0)

static int udp(const char *ip, int port) {
	int fd = socket(AF_INET, SOCK_DGRAM, 0), one = 1;
	setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
	struct sockaddr_in a = {.sin_family = AF_INET, .sin_port = htons(port)};
	inet_pton(AF_INET, ip, &a.sin_addr);
	if (bind(fd, (void *)&a, sizeof a) != 0)
		printf("bind %s:%d: %s\n", ip, port, strerror(errno));
	return fd;
}

static int join(int fd, const char *group, const char *ifaddr, int opt) {
	struct ip_mreq m = {0};
	inet_pton(AF_INET, group, &m.imr_multiaddr);
	inet_pton(AF_INET, ifaddr, &m.imr_interface);
	return ERR(setsockopt(fd, IPPROTO_IP, opt, &m, sizeof m));
}

static struct sockaddr_in grp(int port) {
	struct sockaddr_in g = {.sin_family = AF_INET, .sin_port = htons(port)};
	inet_pton(AF_INET, "224.0.0.251", &g.sin_addr);
	return g;
}

// got reports whether fd has a datagram, taking it.
static int got(int fd) {
	char b[8];
	return recv(fd, b, sizeof b, MSG_DONTWAIT) > 0;
}

static int optval(int fd, int level, int name) {
	int v = -7;
	socklen_t l = sizeof v;
	getsockopt(fd, level, name, &v, &l);
	return v;
}

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	const int port = 5355;

	int u = udp("0.0.0.0", 0);
	CHECK(optval(u, IPPROTO_IP, IP_MULTICAST_TTL) == 1, "IP_MULTICAST_TTL default %d", optval(u, IPPROTO_IP, IP_MULTICAST_TTL));
	CHECK(optval(u, IPPROTO_IP, IP_MULTICAST_LOOP) == 1, "IP_MULTICAST_LOOP default %d", optval(u, IPPROTO_IP, IP_MULTICAST_LOOP));
	CHECK(optval(u, IPPROTO_IP, IP_TTL) == 64, "IP_TTL default %d", optval(u, IPPROTO_IP, IP_TTL));

	int e;
	CHECK((e = join(u, "224.0.0.251", "0.0.0.0", IP_ADD_MEMBERSHIP)) == ENODEV, "join with no route: %s", strerror(e));
	CHECK((e = join(u, "10.0.0.1", "127.0.0.1", IP_ADD_MEMBERSHIP)) == EINVAL, "join a unicast group: %s", strerror(e));
	int t = socket(AF_INET, SOCK_STREAM, 0);
	CHECK((e = join(t, "224.0.0.251", "127.0.0.1", IP_ADD_MEMBERSHIP)) == EPROTO, "join on TCP: %s", strerror(e));
	close(t);
	struct sockaddr_in g = grp(port);
	CHECK((e = ERR(sendto(u, "x", 1, 0, (void *)&g, sizeof g))) == ENETUNREACH, "send with no route: %s", strerror(e));
	CHECK((e = join(u, "224.0.0.251", "127.0.0.1", IP_ADD_MEMBERSHIP)) == 0, "join on lo: %s", strerror(e));
	CHECK((e = join(u, "224.0.0.251", "127.0.0.1", IP_ADD_MEMBERSHIP)) == EADDRINUSE, "join again: %s", strerror(e));
	CHECK((e = join(u, "224.0.0.252", "127.0.0.1", IP_DROP_MEMBERSHIP)) == EADDRNOTAVAIL, "leave unjoined: %s", strerror(e));

	struct in_addr lo = {0};
	inet_pton(AF_INET, "127.0.0.1", &lo);
	CHECK((e = ERR(setsockopt(u, IPPROTO_IP, IP_MULTICAST_IF, &lo, sizeof lo))) == 0, "IP_MULTICAST_IF lo: %s", strerror(e));
	struct in_addr ifa = {0};
	socklen_t il = sizeof ifa;
	getsockopt(u, IPPROTO_IP, IP_MULTICAST_IF, &ifa, &il);
	CHECK(ifa.s_addr == lo.s_addr, "IP_MULTICAST_IF reads %s", inet_ntoa(ifa));

	// Bound to the port: on the wildcard without joining (IP_MULTICAST_ALL),
	// on the group having joined, and one that takes only what it joined.
	int any = udp("0.0.0.0", port), on = udp("224.0.0.251", port), only = udp("0.0.0.0", port);
	CHECK(join(on, "224.0.0.251", "127.0.0.1", IP_ADD_MEMBERSHIP) == 0, "join on the group's socket");
	int off = 0;
	setsockopt(only, IPPROTO_IP, IP_MULTICAST_ALL, &off, sizeof off);
	CHECK((e = ERR(sendto(u, "x", 1, 0, (void *)&g, sizeof g))) == 0, "send via lo: %s", strerror(e));
	struct sockaddr_in from = {0};
	socklen_t fl = sizeof from;
	char b[8];
	CHECK(recvfrom(any, b, sizeof b, MSG_DONTWAIT, (void *)&from, &fl) == 1, "the wildcard socket got nothing");
	CHECK(from.sin_addr.s_addr == lo.s_addr, "from %s", inet_ntoa(from.sin_addr));
	CHECK(got(on), "the group's socket got nothing");
	CHECK(!got(only), "a socket without IP_MULTICAST_ALL got a group it did not join");
	CHECK(!got(u), "the sender, on another port, got its own");

	// Out of lo, IP_MULTICAST_LOOP off or not, the loopback carries it back.
	setsockopt(u, IPPROTO_IP, IP_MULTICAST_LOOP, &off, sizeof off);
	sendto(u, "x", 1, 0, (void *)&g, sizeof g);
	CHECK(got(any) && got(on), "lo did not carry it back with IP_MULTICAST_LOOP off");
	int one = 1;
	setsockopt(u, IPPROTO_IP, IP_MULTICAST_LOOP, &one, sizeof one);

	// Once every socket left, lo takes the group no more.
	CHECK((e = join(u, "224.0.0.251", "127.0.0.1", IP_DROP_MEMBERSHIP)) == 0, "leave: %s", strerror(e));
	CHECK(join(on, "224.0.0.251", "127.0.0.1", IP_DROP_MEMBERSHIP) == 0, "leave on the group's socket");
	sendto(u, "x", 1, 0, (void *)&g, sizeof g);
	CHECK(!got(any), "a group no socket holds was delivered");

	int u6 = socket(AF_INET6, SOCK_DGRAM, 0);
	struct ipv6_mreq m6 = {0};
	inet_pton(AF_INET6, "ff02::fb", &m6.ipv6mr_multiaddr);
	CHECK((e = ERR(setsockopt(u6, IPPROTO_IPV6, IPV6_JOIN_GROUP, &m6, sizeof m6))) == ENODEV, "IPv6 join with no route: %s", strerror(e));
	m6.ipv6mr_interface = if_nametoindex("lo");
	CHECK((e = ERR(setsockopt(u6, IPPROTO_IPV6, IPV6_JOIN_GROUP, &m6, sizeof m6))) == 0, "IPv6 join on lo (%u): %s", m6.ipv6mr_interface, strerror(e));
	CHECK((e = ERR(setsockopt(u6, IPPROTO_IPV6, IPV6_JOIN_GROUP, &m6, sizeof m6))) == EADDRINUSE, "IPv6 join again: %s", strerror(e));
	CHECK((e = ERR(setsockopt(u6, IPPROTO_IPV6, IPV6_LEAVE_GROUP, &m6, sizeof m6))) == 0, "IPv6 leave: %s", strerror(e));

	// A socket bound to a group sends from the route's address, not the group.
	int gs = udp("224.0.0.251", 5356), r = udp("127.0.0.1", 5357);
	struct sockaddr_in to = {.sin_family = AF_INET, .sin_port = htons(5357), .sin_addr = lo};
	sendto(gs, "x", 1, 0, (void *)&to, sizeof to);
	memset(&from, 0, sizeof from);
	fl = sizeof from;
	CHECK(recvfrom(r, b, sizeof b, MSG_DONTWAIT, (void *)&from, &fl) == 1 && from.sin_addr.s_addr == lo.s_addr && ntohs(from.sin_port) == 5356,
		"from a group-bound socket: %s:%d", inet_ntoa(from.sin_addr), ntohs(from.sin_port));

	// IPv6 multicast: lo carries no route for it, so nothing is sent, whatever
	// interface is named; IPV6_MULTICAST_IF takes an interface that exists.
	CHECK(optval(u6, IPPROTO_IPV6, 29 /* IPV6_MULTICAST_ALL */) == 1, "IPV6_MULTICAST_ALL default %d", optval(u6, IPPROTO_IPV6, 29));
	unsigned lo6 = if_nametoindex("lo"), bad = 77;
	CHECK((e = ERR(setsockopt(u6, IPPROTO_IPV6, IPV6_MULTICAST_IF, &bad, sizeof bad))) == ENODEV, "IPV6_MULTICAST_IF 77: %s", strerror(e));
	struct sockaddr_in6 g6 = {.sin6_family = AF_INET6, .sin6_port = htons(port)};
	inet_pton(AF_INET6, "ff02::fb", &g6.sin6_addr);
	CHECK((e = ERR(sendto(u6, "x", 1, 0, (void *)&g6, sizeof g6))) == ENETUNREACH, "IPv6 send with no route: %s", strerror(e));
	CHECK((e = ERR(setsockopt(u6, IPPROTO_IPV6, IPV6_MULTICAST_IF, &lo6, sizeof lo6))) == 0, "IPV6_MULTICAST_IF lo: %s", strerror(e));
	CHECK(optval(u6, IPPROTO_IPV6, IPV6_MULTICAST_IF) == (int)lo6, "IPV6_MULTICAST_IF reads %d", optval(u6, IPPROTO_IPV6, IPV6_MULTICAST_IF));
	CHECK((e = ERR(sendto(u6, "x", 1, 0, (void *)&g6, sizeof g6))) == ENETUNREACH, "IPv6 send out of lo: %s", strerror(e));

	// Binding an IPv6 group: a link-local one takes an interface that exists;
	// one group and interface at a time, as any address.
	int b6 = socket(AF_INET6, SOCK_DGRAM, 0);
	CHECK((e = ERR(bind(b6, (void *)&g6, sizeof g6))) == EINVAL, "bind ff02::fb with no interface: %s", strerror(e));
	g6.sin6_scope_id = bad;
	CHECK((e = ERR(bind(b6, (void *)&g6, sizeof g6))) == ENODEV, "bind ff02::fb%%77: %s", strerror(e));
	g6.sin6_scope_id = lo6;
	CHECK((e = ERR(bind(b6, (void *)&g6, sizeof g6))) == 0, "bind ff02::fb%%lo: %s", strerror(e));
	struct sockaddr_in6 n6 = {0};
	socklen_t nl = sizeof n6;
	getsockname(b6, (void *)&n6, &nl);
	CHECK(!memcmp(&n6.sin6_addr, &g6.sin6_addr, 16) && n6.sin6_port == g6.sin6_port && n6.sin6_scope_id == lo6, "getsockname of ff02::fb%%lo: port %d scope %u", ntohs(n6.sin6_port), n6.sin6_scope_id);
	int b6b = socket(AF_INET6, SOCK_DGRAM, 0), b6c = socket(AF_INET6, SOCK_DGRAM, 0), b6d = socket(AF_INET6, SOCK_DGRAM, 0);
	CHECK((e = ERR(bind(b6b, (void *)&g6, sizeof g6))) == EADDRINUSE, "bind ff02::fb%%lo again: %s", strerror(e));
	struct sockaddr_in6 w6 = {.sin6_family = AF_INET6, .sin6_port = htons(port)};
	CHECK((e = ERR(bind(b6c, (void *)&w6, sizeof w6))) == EADDRINUSE, "bind [::] over ff02::fb%%lo: %s", strerror(e));
	inet_pton(AF_INET6, "ff02::fc", &g6.sin6_addr);
	CHECK((e = ERR(bind(b6b, (void *)&g6, sizeof g6))) == 0, "bind ff02::fc%%lo: %s", strerror(e));
	inet_pton(AF_INET6, "ff0e::fb", &g6.sin6_addr);
	g6.sin6_scope_id = 0;
	CHECK((e = ERR(bind(b6d, (void *)&g6, sizeof g6))) == 0, "bind ff0e::fb: %s", strerror(e));

	printf("vmcast fails=%d\n", fails);
	return fails != 0;
}
