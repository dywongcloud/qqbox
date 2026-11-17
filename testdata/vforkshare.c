// vforkshare: a vfork child shares its parent's memory and runs on its stack,
// as on Linux: what it writes, the parent sees once it resumes; the parent's
// own state survives the child's use of the stack below its sp; and a child
// that execs leaves the parent to go on.
#include <stdio.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

static volatile int global;

// scribble uses n bytes of stack below the caller's.
static void __attribute__((noinline)) scribble(int n) {
	volatile char pad[16384];
	memset((char *)pad, 0x5a, n < (int)sizeof pad ? n : (int)sizeof pad);
}

int main(int argc, char **argv) {
	if (argc > 1)
		return 0; // the exec'd child
	volatile int local = 1;
	volatile unsigned long marks[8];
	for (int i = 0; i < 8; i++)
		marks[i] = 0x0101010101010101UL * (i + 1);
	pid_t c = vfork();
	if (c == 0) {
		global = 42;
		local = 7;
		scribble(16384);
		_exit(0);
	}
	int st = 0;
	CHECK(waitpid(c, &st, 0) == c && WIFEXITED(st) && WEXITSTATUS(st) == 0, "the vfork child: status %#x", st);
	CHECK(global == 42, "the parent sees the child's write to a global: %d", global);
	CHECK(local == 7, "the parent sees the child's write to its own frame: %d", local);
	for (int i = 0; i < 8; i++)
		CHECK(marks[i] == 0x0101010101010101UL * (i + 1), "the parent's frame survives the child: mark %d", i);

	c = vfork();
	if (c == 0) {
		execl("/proc/self/exe", "vforkshare", "child", (char *)0);
		_exit(99);
	}
	CHECK(waitpid(c, &st, 0) == c && WIFEXITED(st) && WEXITSTATUS(st) == 0, "the vfork child that execs: status %#x", st);
	CHECK(local == 7 && global == 42, "the parent goes on after an exec'd child");
	printf("vforkshare fails=%d\n", fails);
	return fails != 0;
}
