// argfault: syscalls whose input pointers are bad, or run into an unmapped
// page, fail with EFAULT (or take what precedes the hole) as on Linux, and an
// input that ends right at a mapping's edge is read whole.
#define _GNU_SOURCE
#include <errno.h>
#include <linux/futex.h>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/epoll.h>
#include <sys/mman.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/uio.h>
#include <time.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

int main(void) {
	long pg = sysconf(_SC_PAGESIZE);
	// Two pages, the second inaccessible: an input running past the first
	// faults there.
	char *m = mmap(0, 2 * pg, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	CHECK(m != MAP_FAILED && mprotect(m + pg, pg, PROT_NONE) == 0, "map");
	char *edge = m + pg;

	int p[2];
	CHECK(pipe(p) == 0, "pipe");
	errno = 0;
	CHECK(write(p[1], 0, 10) == -1 && errno == EFAULT, "write NULL: errno %d", errno);
	// A pipe drops the page it was filling when the copy faults: a write
	// shorter than a page that runs into the hole writes nothing.
	memset(edge - 8, 'a', 8);
	ssize_t n = write(p[1], edge - 8, 16);
	CHECK(n == -1 && errno == EFAULT, "write into the hole: %zd errno %d", n, errno);
	char buf[32];
	CHECK(write(p[1], edge - 8, 8) == 8, "write ending at the edge");
	CHECK(read(p[0], buf, sizeof buf) == 8 && memcmp(buf, "aaaaaaaa", 8) == 0, "edge bytes");

	struct iovec iv[2] = {{edge - 4, 4}, {0, 4}};
	errno = 0;
	n = writev(p[1], iv, 2);
	CHECK(n == -1 && errno == EFAULT, "writev with a bad iovec: %zd errno %d", n, errno);
	// A regular file keeps what it got before the fault.
	char tmpl[] = "/tmp/argfaultXXXXXX";
	int tf = mkstemp(tmpl);
	CHECK(tf >= 0 && unlink(tmpl) == 0, "temp file");
	errno = 0;
	n = writev(tf, iv, 2);
	CHECK(n == 4, "writev to a file with a bad iovec: %zd errno %d", n, errno);
	CHECK(lseek(tf, 0, SEEK_END) == 4, "the file has the bytes before the fault");

	// Reads the other way: a pipe read whose buffer runs into the hole fails
	// and leaves the data in the pipe; a file read gets what fits and moves
	// the offset that far; a positional one gets what fits.
	CHECK(write(p[1], "abcdefgh", 8) == 8, "fill the pipe");
	errno = 0;
	n = read(p[0], edge - 4, 8);
	CHECK(n == -1 && errno == EFAULT, "read into the hole: %zd errno %d", n, errno);
	errno = 0;
	n = readv(p[0], iv, 2);
	CHECK(n == -1 && errno == EFAULT, "readv with a bad iovec: %zd errno %d", n, errno);
	CHECK(read(p[0], buf, sizeof buf) == 8 && memcmp(buf, "abcdefgh", 8) == 0, "the pipe kept what the faulting reads could not take");
	CHECK(pwrite(tf, "0123456789", 10, 0) == 10 && lseek(tf, 0, SEEK_SET) == 0, "fill the file");
	errno = 0;
	n = read(tf, edge - 4, 8);
	CHECK(n == 4 && memcmp(edge - 4, "0123", 4) == 0, "file read into the hole: %zd errno %d", n, errno);
	CHECK(lseek(tf, 0, SEEK_CUR) == 4, "the offset moved past what the read got: %ld", (long)lseek(tf, 0, SEEK_CUR));
	errno = 0;
	n = pread(tf, edge - 4, 8, 6);
	CHECK(n == 4 && memcmp(edge - 4, "6789", 4) == 0, "pread into the hole: %zd errno %d", n, errno);

	// epoll_wait puts back what it cannot write: an edge-triggered event
	// survives a wait whose buffer is all in the hole, and a buffer with
	// room for one of two takes one, then the other.
	int ep = epoll_create1(0), q[2];
	CHECK(ep >= 0 && pipe(q) == 0, "epoll and a second pipe");
	struct epoll_event ev = {.events = EPOLLIN | EPOLLET, .data.u32 = 1}, got[2];
	CHECK(epoll_ctl(ep, EPOLL_CTL_ADD, p[0], &ev) == 0, "register the first pipe");
	ev.data.u32 = 2;
	CHECK(epoll_ctl(ep, EPOLL_CTL_ADD, q[0], &ev) == 0, "register the second pipe");
	CHECK(write(p[1], "x", 1) == 1, "ready the first");
	errno = 0;
	n = epoll_wait(ep, (struct epoll_event *)edge, 2, 0);
	CHECK(n == -1 && errno == EFAULT, "epoll_wait into the hole: %zd errno %d", n, errno);
	CHECK(epoll_wait(ep, got, 2, 0) == 1 && got[0].data.u32 == 1, "the edge kept for the next wait");
	CHECK(write(q[1], "y", 1) == 1 && write(p[1], "x", 1) == 1, "ready both");
	struct epoll_event *one = (struct epoll_event *)(edge - sizeof(struct epoll_event));
	CHECK(epoll_wait(ep, one, 2, 0) == 1, "room for one");
	CHECK(epoll_wait(ep, got, 2, 0) == 1 && got[0].data.u32 != one->data.u32, "then the other");
	close(ep);
	close(q[0]);
	close(q[1]);
	close(tf);
	errno = 0;
	CHECK(writev(p[1], (struct iovec *)(edge - 8), 2) == -1 && errno == EFAULT, "iovec array into the hole: errno %d", errno);

	// A path whose NUL is the last byte before the hole is whole; one that
	// runs into it is a fault.
	strcpy(edge - 6, "/nope");
	errno = 0;
	CHECK(open(edge - 6, O_RDONLY) == -1 && errno == ENOENT, "path at the edge: errno %d", errno);
	memset(edge - 6, 'x', 6);
	errno = 0;
	CHECK(open(edge - 6, O_RDONLY) == -1 && errno == EFAULT, "path into the hole: errno %d", errno);
	errno = 0;
	CHECK(open(0, O_RDONLY) == -1 && errno == EFAULT, "open NULL: errno %d", errno);
	struct stat st;
	errno = 0;
	CHECK(stat(edge, &st) == -1 && errno == EFAULT, "stat in the hole: errno %d", errno);

	ep = epoll_create1(0);
	errno = 0;
	CHECK(epoll_ctl(ep, EPOLL_CTL_ADD, p[0], (struct epoll_event *)(edge - 4)) == -1 && errno == EFAULT, "epoll_ctl event into the hole: errno %d", errno);
	CHECK(epoll_ctl(ep, EPOLL_CTL_ADD, p[0], (struct epoll_event *)(edge - sizeof(struct epoll_event))) == 0, "epoll_ctl event at the edge");
	CHECK(epoll_ctl(ep, EPOLL_CTL_DEL, p[0], 0) == 0, "epoll_ctl DEL with no event");

	errno = 0;
	CHECK(nanosleep((struct timespec *)(edge - 8), 0) == -1 && errno == EFAULT, "nanosleep into the hole: errno %d", errno);
	struct pollfd *pf = (struct pollfd *)(edge - 8);
	pf->fd = p[0];
	pf->events = POLLIN;
	errno = 0;
	CHECK(poll(pf, 2, 0) == -1 && errno == EFAULT, "poll array into the hole: errno %d", errno);
	CHECK(poll(pf, 1, 0) >= 0, "poll array at the edge: errno %d", errno);

	int s[2];
	CHECK(socketpair(AF_UNIX, SOCK_DGRAM, 0, s) == 0, "socketpair");
	errno = 0;
	CHECK(sendto(s[0], "x", 1, 0, (struct sockaddr *)(edge - 2), 16) == -1 && errno == EFAULT, "sendto address into the hole: errno %d", errno);
	sigset_t *ss = (sigset_t *)(edge - 4);
	errno = 0;
	CHECK(syscall(SYS_rt_sigprocmask, SIG_BLOCK, ss, 0, 8) == -1 && errno == EFAULT, "sigprocmask into the hole: errno %d", errno);

	// Calls a stub may serve itself, with no supervisor to read for it.
	errno = 0;
	CHECK(syscall(SYS_futex, edge, FUTEX_WAIT, 0, 0, 0, 0) == -1 && errno == EFAULT, "futex on the hole: errno %d", errno);
	*(int *)(edge - 4) = 0;
	errno = 0;
	CHECK(syscall(SYS_futex, edge - 4, FUTEX_WAIT, 0, edge - 8, 0, 0) == -1 && errno == EFAULT, "futex timeout into the hole: errno %d", errno);
	errno = 0;
	CHECK(syscall(SYS_clock_gettime, CLOCK_MONOTONIC, edge - 8) == -1 && errno == EFAULT, "clock_gettime into the hole: errno %d", errno);
	errno = 0;
	CHECK(syscall(SYS_gettimeofday, edge - 8, 0) == -1 && errno == EFAULT, "gettimeofday into the hole: errno %d", errno);
	struct timespec ts;
	CHECK(syscall(SYS_clock_gettime, CLOCK_MONOTONIC, &ts) == 0, "clock_gettime");

	printf("argfault fails=%d\n", fails);
	return fails != 0;
}
