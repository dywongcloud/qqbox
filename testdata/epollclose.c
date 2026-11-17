// epollclose: an epoll registration lasts as long as its open file, as Linux
// keeps it: closing one of two descriptors of a file keeps it reporting,
// closing the last removes it and releases the file, so its peer sees end of
// file. EPOLL_CTL_MOD changes the events in place.
#include <errno.h>
#include <poll.h>
#include <stdio.h>
#include <sys/epoll.h>
#include <sys/socket.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

// peerEOF reports whether fd's peer is gone: end of file, or a reset when the
// peer closed with data still queued for it.
static int peerEOF(int fd) {
	struct pollfd p = {.fd = fd, .events = POLLIN};
	char c;
	if (poll(&p, 1, 1000) != 1 || !(p.revents & (POLLIN | POLLHUP)))
		return 0;
	ssize_t r = read(fd, &c, 1);
	return r == 0 || (r < 0 && errno == ECONNRESET);
}

int main(void) {
	int sv[2];
	socketpair(AF_UNIX, SOCK_STREAM, 0, sv);
	int ep = epoll_create1(0);
	struct epoll_event e = {.events = EPOLLOUT, .data.u64 = 7};
	CHECK(epoll_ctl(ep, EPOLL_CTL_ADD, sv[0], &e) == 0, "add");
	struct epoll_event out[4];
	CHECK(epoll_wait(ep, out, 4, 0) == 1 && out[0].events == EPOLLOUT && out[0].data.u64 == 7, "writable");
	e.events = EPOLLIN;
	e.data.u64 = 8;
	CHECK(epoll_ctl(ep, EPOLL_CTL_MOD, sv[0], &e) == 0, "mod");
	CHECK(epoll_wait(ep, out, 4, 0) == 0, "not readable yet");
	CHECK(write(sv[1], "x", 1) == 1, "write");
	CHECK(epoll_wait(ep, out, 4, 0) == 1 && out[0].events == EPOLLIN && out[0].data.u64 == 8, "readable after mod");

	// Another descriptor of the file keeps the registration.
	int dup0 = dup(sv[0]);
	close(sv[0]);
	CHECK(epoll_wait(ep, out, 4, 0) == 1 && out[0].data.u64 == 8, "registration kept while a dup is open");
	close(dup0);
	CHECK(peerEOF(sv[1]), "peer sees EOF once the last descriptor closes");
	CHECK(epoll_wait(ep, out, 4, 0) == 0, "no events from a released file");

	// A file closed with no dup leaves the epoll at once.
	int sv2[2];
	socketpair(AF_UNIX, SOCK_STREAM, 0, sv2);
	e.events = EPOLLIN | EPOLLOUT;
	CHECK(epoll_ctl(ep, EPOLL_CTL_ADD, sv2[0], &e) == 0, "add 2");
	close(sv2[0]);
	CHECK(peerEOF(sv2[1]), "peer of a closed registered socket sees EOF");
	CHECK(epoll_wait(ep, out, 4, 0) == 0, "closed file reports nothing");
	printf("epollclose fails=%d\n", fails);
	return fails != 0;
}
