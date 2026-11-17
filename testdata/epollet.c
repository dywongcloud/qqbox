// Edge-triggered epoll must not lose a ready fd when more are ready than a
// wait's maxevents: the host reports an edge once, so the supervisor has to
// hold the excess over to the next wait. Two eventfds, both readable and
// registered EPOLLIN|EPOLLET, polled one at a time, must both be seen.
// Returns 0 on success, else the line number of the failed check.
#define _GNU_SOURCE
#include <sys/epoll.h>
#include <sys/eventfd.h>
#include <unistd.h>
#include <stdint.h>

#define CHECK(c) do { if (!(c)) return __LINE__; } while (0)

int main(void) {
	int ep = epoll_create1(0);
	CHECK(ep >= 0);
	int a = eventfd(1, EFD_NONBLOCK), b = eventfd(1, EFD_NONBLOCK);
	CHECK(a >= 0 && b >= 0);
	struct epoll_event ev = { .events = EPOLLIN | EPOLLET, .data.fd = a };
	CHECK(epoll_ctl(ep, EPOLL_CTL_ADD, a, &ev) == 0);
	ev.data.fd = b;
	CHECK(epoll_ctl(ep, EPOLL_CTL_ADD, b, &ev) == 0);

	int seen_a = 0, seen_b = 0;
	for (int i = 0; i < 2; i++) {
		struct epoll_event out[1];
		int n = epoll_wait(ep, out, 1, 1000); // maxevents=1, both are ready
		CHECK(n == 1);                         // each wait must return one
		if (out[0].data.fd == a) seen_a++;
		else if (out[0].data.fd == b) seen_b++;
	}
	CHECK(seen_a == 1 && seen_b == 1); // both edges delivered, neither lost
	return 0;
}
