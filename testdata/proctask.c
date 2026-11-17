// /proc/<pid>/task link count. Linux reports a directory's st_nlink as 2 (for
// "." and "..") plus one per subdirectory, so a task directory is 2 + the
// thread count. Chromium's sandbox CHECKs st_nlink >= 3 there to confirm the
// process is single-threaded before entering a namespace; a wrong count aborts
// the GPU process. Returns 0 on success, else the line number of the failed
// check.
#define _GNU_SOURCE
#include <sys/stat.h>
#include <pthread.h>
#include <stdatomic.h>
#include <unistd.h>

#define CHECK(c) do { if (!(c)) return __LINE__; } while (0)

static atomic_int stop;
static void *park(void *_) { while (!atomic_load(&stop)) usleep(1000); return 0; }

static unsigned long nlink(void) {
	struct stat st;
	if (stat("/proc/self/task", &st) != 0) return 0;
	return st.st_nlink;
}

// waitNlink waits for the link count to settle at want (threads appear in and
// leave /proc/self/task asynchronously).
static unsigned long waitNlink(unsigned long want) {
	for (int i = 0; i < 200 && nlink() != want; i++) usleep(1000);
	return nlink();
}

int main(void) {
	// Single-threaded: 2 (. and ..) + 1 thread == 3, the value Chromium requires.
	CHECK(nlink() == 3);

	pthread_t t[2];
	for (int i = 0; i < 2; i++)
		CHECK(pthread_create(&t[i], 0, park, 0) == 0);
	CHECK(waitNlink(5) == 5);  // 2 + 3 threads

	atomic_store(&stop, 1);
	for (int i = 0; i < 2; i++) CHECK(pthread_join(t[i], 0) == 0);
	CHECK(waitNlink(3) == 3);  // back to single-threaded after the joins
	return 0;
}
