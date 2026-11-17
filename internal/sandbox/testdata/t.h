// t.h — tiny check harness for the process-model guest tests.
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

static int fails;
#define CHECK(cond, ...)                                                   \
    do {                                                                   \
        if (!(cond)) {                                                     \
            fails++;                                                       \
            printf("FAIL %s:%d: %s: ", __func__, __LINE__, #cond);         \
            printf(__VA_ARGS__);                                           \
            printf(" (errno %d)\n", errno);                                \
            fflush(stdout);                                                \
        }                                                                  \
    } while (0)
#define DONE()                                                             \
    do {                                                                   \
        printf(fails ? "FAILED %d\n" : "ALL OK\n", fails);                 \
        fflush(stdout);                                                    \
        return fails != 0;                                                 \
    } while (0)

static double now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return ts.tv_sec + ts.tv_nsec / 1e9;
}

static void msleep(int ms) {
    struct timespec ts = {ms / 1000, (ms % 1000) * 1000000L};
    nanosleep(&ts, 0);
}

// readall reads fd to EOF into buf (NUL-terminated), returning the length.
static int readall(int fd, char *buf, int cap) {
    int n = 0, r;
    while (n < cap - 1 && (r = read(fd, buf + n, cap - 1 - n)) > 0) n += r;
    buf[n] = 0;
    return n;
}

static void writefile(const char *path, const char *s, int mode) {
    unlink(path);
    int fd = open(path, O_CREAT | O_WRONLY | O_TRUNC, mode);
    if (fd < 0) { printf("FAIL writefile %s errno %d\n", path, errno); fails++; return; }
    write(fd, s, strlen(s));
    close(fd);
}

// run forks, execs argv with env (NULL = inherit), and captures stdout.
static int run(char *const argv[], char *const envp[], char *out, int cap) {
    int p[2];
    pipe(p);
    fflush(stdout);
    pid_t pid = fork();
    if (pid == 0) {
        dup2(p[1], 1);
        close(p[0]);
        close(p[1]);
        if (envp) execve(argv[0], argv, envp);
        else execv(argv[0], argv);
        _exit(127);
    }
    close(p[1]);
    readall(p[0], out, cap);
    close(p[0]);
    int st = 0;
    waitpid(pid, &st, 0);
    return st;
}
