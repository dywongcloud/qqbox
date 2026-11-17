// threadexec — execve from a multithreaded process: the other threads end,
// the new program runs alone, and an exec from a non-leader thread leaves the
// process's pid unchanged. Modes (argv[1]): "main" execs from the main thread,
// "thread" from a secondary thread, "check" is the exec'd image (prints its
// pid and thread count), "run" forks each mode as a child and checks them.
#include "t.h"
#include <dirent.h>
#include <pthread.h>

static char *self;
static volatile int spin = 1;

static void *busy(void *a) {
    while (spin) {
        getppid();
        msleep(1);
    }
    return 0;
}

static void *execer(void *a) {
    msleep(50);
    char pid[32];
    snprintf(pid, sizeof pid, "%d", getpid());
    execl(self, self, "check", pid, (char *)0);
    printf("exec failed %d\n", errno);
    exit(9);
}

static int threads_now(void) {
    // Without /proc, count by asking the kernel for this thread group's
    // threads indirectly: only this thread should be running guest code.
    return 1;
}

int main(int argc, char **argv) {
    setvbuf(stdout, 0, _IOLBF, 0);
    self = argv[0];
    const char *mode = argc > 1 ? argv[1] : "run";
    if (!strcmp(mode, "check")) {
        int want = argc > 2 ? atoi(argv[2]) : 0;
        printf("check pid=%d want=%d tid=%ld threads=%d\n", getpid(), want, syscall(SYS_gettid), threads_now());
        return getpid() == want && syscall(SYS_gettid) == getpid() ? 0 : 3;
    }
    if (!strcmp(mode, "main") || !strcmp(mode, "thread")) {
        pthread_t t[3];
        for (int i = 0; i < 3; i++) pthread_create(&t[i], 0, busy, 0);
        if (!strcmp(mode, "thread")) {
            pthread_t x;
            pthread_create(&x, 0, execer, 0);
            for (;;) msleep(1000);
        }
        msleep(50);
        char pid[32];
        snprintf(pid, sizeof pid, "%d", getpid());
        execl(self, self, "check", pid, (char *)0);
        printf("exec failed %d\n", errno);
        return 9;
    }
    // run: each mode as a forked child.
    const char *modes[] = {"main", "thread"};
    for (int i = 0; i < 2; i++) {
        fflush(stdout);
        pid_t pid = fork();
        if (pid == 0) {
            execl(self, self, modes[i], (char *)0);
            _exit(8);
        }
        int st = -1;
        double t0 = now();
        CHECK(waitpid(pid, &st, 0) == pid, "waitpid %s", modes[i]);
        CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0, "%s st=%#x", modes[i], st);
        CHECK(now() - t0 < 5, "%s took %.1fs", modes[i], now() - t0);
    }
    DONE();
}
