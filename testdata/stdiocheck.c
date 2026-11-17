// stdiocheck: the guest's view of discarded stdio. Run with stdin, stdout and
// stderr all discarded; the exit status is the line of the first failed
// check, 0 if none failed.
//
// "stdiocheck write N" instead writes 4 KiB to stdout N times, for timing.
#include <fcntl.h>
#include <stdlib.h>
#include <poll.h>
#include <pthread.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/sendfile.h>
#include <sys/stat.h>
#include <sys/sysmacros.h>
#include <sys/uio.h>
#include <sys/wait.h>
#include <unistd.h>

static int first;
#define CHECK(c) do { if (!(c) && !first) first = __LINE__; } while (0)

static char buf[1 << 16];

static int isnull(int fd) {
	struct stat st;
	return fstat(fd, &st) == 0 && S_ISCHR(st.st_mode) && major(st.st_rdev) == 1 && minor(st.st_rdev) == 3;
}

static void *writer(void *arg) {
	(void)arg;
	for (int i = 0; i < 1000; i++)
		if (write(1, buf, 100) != 100) return (void *)1;
	return 0;
}

int main(int argc, char **argv) {
	if (argc == 3 && !strcmp(argv[1], "write")) {
		for (long n = atol(argv[2]); n > 0; n--)
			if (write(1, buf, 4096) != 4096) return 1;
		return 0;
	}
	for (int fd = 0; fd < 3; fd++) CHECK(isnull(fd));
	CHECK(write(1, buf, sizeof buf) == sizeof buf);
	CHECK(write(2, buf, 7) == 7);
	CHECK(write(1, buf, 0) == 0);
	struct iovec iov[3] = {{buf, 10}, {buf, 0}, {buf, 5000}};
	CHECK(writev(1, iov, 3) == 5010);
	CHECK(read(0, buf, sizeof buf) == 0);
	CHECK(pwrite(1, buf, 9, 100) == 9);

	struct pollfd p[2] = {{1, POLLOUT, 0}, {0, POLLIN, 0}};
	CHECK(poll(p, 2, 0) == 2 && (p[0].revents & POLLOUT) && (p[1].revents & POLLIN));

	// libuv marks a stream readable or writable from the access mode, and
	// sets it nonblocking with FIONBIO.
	CHECK((fcntl(0, F_GETFL) & O_ACCMODE) != O_WRONLY);
	CHECK((fcntl(1, F_GETFL) & O_ACCMODE) != O_RDONLY);
	int on = 1, off0 = 0;
	CHECK(ioctl(1, FIONBIO, &on) == 0 && ioctl(1, FIONBIO, &off0) == 0);
	CHECK(ioctl(2, FIOCLEX) == 0 && ioctl(2, FIONCLEX) == 0);

	pthread_t th;
	void *r = (void *)1;
	CHECK(pthread_create(&th, 0, writer, 0) == 0 && pthread_join(th, &r) == 0 && r == 0);

	// A file put on fd 1 gets what is written there; fd 1 back on the null
	// device discards again.
	int saved = dup(1);
	CHECK(isnull(saved));
	int f = open("/tmp/stdiocheck.out", O_RDWR | O_CREAT | O_TRUNC, 0644);
	CHECK(f >= 0);
	CHECK(dup2(f, 1) == 1);
	CHECK(write(1, "data", 4) == 4);
	struct iovec v2[2] = {{"ab", 2}, {"cd", 2}};
	CHECK(writev(1, v2, 2) == 4);
	char got[16] = {0};
	CHECK(pread(f, got, sizeof got, 0) == 8 && memcmp(got, "dataabcd", 8) == 0);
	CHECK(dup2(saved, 1) == 1 && isnull(1));
	CHECK(write(1, "gone", 4) == 4);
	off_t off = 0;
	CHECK(sendfile(1, f, &off, 8) == 8 && off == 8);
	CHECK(lseek(f, 0, SEEK_END) == 8);

	// Closed, fd 1 is EBADF; the next open lands there and gets the writes.
	close(1);
	CHECK(write(1, "x", 1) < 0);
	int g = open("/tmp/stdiocheck.out2", O_RDWR | O_CREAT | O_TRUNC, 0644);
	CHECK(g == 1);
	CHECK(write(1, "zz", 2) == 2);
	CHECK(pread(g, got, sizeof got, 0) == 2);
	dup2(saved, 1);

	// A child shares the description; its exec keeps it.
	pid_t c = fork();
	if (c == 0) {
		if (!isnull(1) || write(1, buf, 3) != 3) _exit(1);
		execl("/bin/sh", "sh", "-c", "exec 3>/tmp/stdiocheck.out3; exec 1>&3; echo child", (char *)0);
		_exit(2);
	}
	int st = -1;
	CHECK(waitpid(c, &st, 0) == c && WIFEXITED(st) && WEXITSTATUS(st) == 0);
	int h = open("/tmp/stdiocheck.out3", O_RDONLY);
	CHECK(h >= 0 && read(h, got, sizeof got) == 6 && memcmp(got, "child\n", 6) == 0);

	return first;
}
