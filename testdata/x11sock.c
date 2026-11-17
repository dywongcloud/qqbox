// Connect to a host AF_UNIX socket under /tmp/.X11-unix/ (where an X server
// listens), the path laboon's -x11 passthrough opens up. argv[1] is the socket
// path. Sends "ping", expects "pong". Returns 0 on success, else the failing
// line; a refused connect (no -x11) exits at the connect check.
#define _GNU_SOURCE
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <string.h>

#define CHECK(c) do { if (!(c)) return __LINE__; } while (0)

int main(int argc, char **argv) {
	CHECK(argc >= 2);
	int s = socket(AF_UNIX, SOCK_STREAM, 0);
	CHECK(s >= 0);
	struct sockaddr_un a = {0};
	a.sun_family = AF_UNIX;
	strncpy(a.sun_path, argv[1], sizeof a.sun_path - 1);
	CHECK(connect(s, (void *)&a, sizeof a) == 0); // refused without -x11
	CHECK(write(s, "ping", 4) == 4);
	char b[8] = {0};
	CHECK(read(s, b, sizeof b) == 4);
	CHECK(memcmp(b, "pong", 4) == 0);
	return 0;
}
