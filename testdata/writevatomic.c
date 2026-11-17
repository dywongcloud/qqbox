// writevatomic: a writev to a pipe of no more than PIPE_BUF bytes is one
// atomic write, however many iovecs it has: two processes writing records of
// two iovecs each never interleave within a record, and each record holds its
// iovecs in order (a lower-case half, then the upper-case one).
#include <ctype.h>
#include <stdio.h>
#include <string.h>
#include <sys/uio.h>
#include <sys/wait.h>
#include <unistd.h>

#define N 2000
#define HALF 128

static void writer(int fd, char c) {
	char a[HALF], b[HALF];
	memset(a, c, HALF);
	memset(b, toupper(c), HALF);
	struct iovec iv[2] = {{a, HALF}, {b, HALF}};
	for (int i = 0; i < N; i++)
		if (writev(fd, iv, 2) != 2 * HALF)
			_exit(1);
	_exit(0);
}

int main(void) {
	int p[2];
	if (pipe(p) != 0)
		return 1;
	pid_t w1 = fork();
	if (w1 == 0)
		writer(p[1], 'a');
	pid_t w2 = fork();
	if (w2 == 0)
		writer(p[1], 'b');
	close(p[1]);
	char rec[2 * HALF];
	int bad = 0, recs = 0;
	for (;;) {
		size_t got = 0;
		while (got < sizeof rec) {
			ssize_t n = read(p[0], rec + got, sizeof rec - got);
			if (n <= 0)
				break;
			got += n;
		}
		if (got < sizeof rec)
			break;
		recs++;
		for (size_t i = 0; i < sizeof rec; i++)
			if (rec[i] != (i < HALF ? rec[0] : toupper(rec[0])) || islower(rec[0]) == 0) {
				bad++;
				break;
			}
	}
	int s1, s2;
	waitpid(w1, &s1, 0);
	waitpid(w2, &s2, 0);
	int fails = bad + (recs != 2 * N) + !(WIFEXITED(s1) && WEXITSTATUS(s1) == 0) + !(WIFEXITED(s2) && WEXITSTATUS(s2) == 0);
	printf("records %d, interleaved %d\nwritevatomic fails=%d\n", recs, bad, fails);
	return fails != 0;
}
