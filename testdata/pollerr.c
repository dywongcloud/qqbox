// A pipe's write end whose readers are gone reports POLLERR to poll, even
// when only POLLIN is asked, and is readable and writable to select.
#include <poll.h>
#include <stdio.h>
#include <sys/select.h>
#include <unistd.h>

int main(void) {
	int p[2];
	if (pipe(p)) return 1;
	close(p[0]);
	struct pollfd f = {p[1], POLLIN, 0};
	int pr = poll(&f, 1, 1000);
	fd_set r, w;
	FD_ZERO(&r);
	FD_ZERO(&w);
	FD_SET(p[1], &r);
	FD_SET(p[1], &w);
	struct timeval tv = {1, 0};
	int sr = select(p[1] + 1, &r, &w, NULL, &tv);
	printf("poll=%d err=%d select=%d r=%d w=%d\n", pr, (f.revents & POLLERR) != 0, sr, FD_ISSET(p[1], &r) != 0, FD_ISSET(p[1], &w) != 0);
	return 0;
}
