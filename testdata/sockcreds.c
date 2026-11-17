// Credential passing over AF_UNIX, the mechanism Chromium's zygote "pid oracle"
// relies on: a socketpair with SO_PASSCRED set on the receiver, a forked child
// that sends a byte, and the parent recovering the child's real pid from the
// SCM_CREDENTIALS attached to that message. Also checks SO_PEERCRED. Returns 0
// on success, else the line number of the failed check.
#define _GNU_SOURCE
#include <sys/socket.h>
#include <sys/wait.h>
#include <unistd.h>
#include <string.h>
#include <stdlib.h>

#define CHECK(c) do { if (!(c)) return __LINE__; } while (0)

static int one_type(int type) {
	int sv[2];
	CHECK(socketpair(AF_UNIX, type, 0, sv) == 0);

	int on = 1;
	CHECK(setsockopt(sv[0], SOL_SOCKET, SO_PASSCRED, &on, sizeof on) == 0);

	pid_t child = fork();
	CHECK(child >= 0);
	if (child == 0) {
		// The child sends one byte; the kernel (here, the supervisor) stamps
		// its credentials because the peer asked for them with SO_PASSCRED.
		char b = 'x';
		ssize_t n = send(sv[1], &b, 1, 0);
		_exit(n == 1 ? 0 : 1);
	}

	char buf[8];
	char cbuf[CMSG_SPACE(sizeof(struct ucred))];
	struct iovec iov = { buf, sizeof buf };
	struct msghdr msg = {0};
	msg.msg_iov = &iov;
	msg.msg_iovlen = 1;
	msg.msg_control = cbuf;
	msg.msg_controllen = sizeof cbuf;

	ssize_t n = recvmsg(sv[0], &msg, 0);
	CHECK(n == 1);
	CHECK(buf[0] == 'x');

	struct cmsghdr *c = CMSG_FIRSTHDR(&msg);
	CHECK(c != NULL);
	CHECK(c->cmsg_level == SOL_SOCKET);
	CHECK(c->cmsg_type == SCM_CREDENTIALS);
	CHECK(c->cmsg_len == CMSG_LEN(sizeof(struct ucred)));
	struct ucred cr;
	memcpy(&cr, CMSG_DATA(c), sizeof cr);
	CHECK(cr.pid == child);  // the oracle: the parent learns the child's real pid
	CHECK(cr.uid == 0);
	CHECK(cr.gid == 0);

	int status = 0;
	CHECK(waitpid(child, &status, 0) == child);
	CHECK(WIFEXITED(status) && WEXITSTATUS(status) == 0);

	// SO_PEERCRED is fixed at socketpair creation: both ends report the
	// creating process (this one).
	struct ucred pc;
	socklen_t pl = sizeof pc;
	CHECK(getsockopt(sv[0], SOL_SOCKET, SO_PEERCRED, &pc, &pl) == 0);
	CHECK(pl == sizeof pc);
	CHECK(pc.pid == getpid());

	close(sv[0]);
	close(sv[1]);
	return 0;
}

int main(void) {
	int types[] = { SOCK_STREAM, SOCK_DGRAM, SOCK_SEQPACKET };
	for (int i = 0; i < 3; i++) {
		int line = one_type(types[i]);
		if (line) return line;  // the failing line (same across types; i tells which)
	}
	return 0;
}
