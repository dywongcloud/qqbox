// killcred.c -- exercises the signal/process_vm credential checks: a same-uid
// signal must work, but a process that dropped to a different uid must not be
// able to signal or read the memory of a process it no longer owns. Prints one
// line per check and KILLCRED-DONE at the end.
#define _GNU_SOURCE
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <signal.h>
#include <errno.h>
#include <sys/uio.h>
#include <sys/syscall.h>

static void onterm(int s) { (void)s; }

int main(void) {
    printf("uid=%d euid=%d\n", getuid(), geteuid());
    fflush(stdout);
    pid_t parent = getpid();

    // same-uid signal must be allowed
    pid_t c1 = fork();
    if (c1 == 0) { signal(SIGTERM, onterm); for (;;) pause(); }
    usleep(100000);
    int r = kill(c1, SIGTERM);
    printf("same-uid-kill rc=%d %s\n", r, r == 0 ? "OK" : strerror(errno));
    kill(c1, SIGKILL);
    waitpid(c1, 0, 0);

    // a child that drops to uid 1000 must NOT be able to signal or read the
    // memory of the (uid 0) parent it no longer owns.
    pid_t c2 = fork();
    if (c2 == 0) {
        if (setuid(1000) != 0) { printf("setuid-failed %s\n", strerror(errno)); fflush(stdout); _exit(0); }
        int rk = kill(parent, SIGTERM);
        printf("xuid-kill rc=%d errno=%s\n", rk, rk < 0 ? strerror(errno) : "ALLOWED");
        char buf[8] = {0};
        struct iovec loc = {buf, 8}, rem = {(void *)main, 8};
        long rp = syscall(SYS_process_vm_readv, parent, &loc, 1, &rem, 1, 0);
        printf("xuid-procvm rc=%ld errno=%s\n", rp, rp < 0 ? strerror(errno) : "ALLOWED");
        fflush(stdout);
        _exit(0);
    }
    waitpid(c2, 0, 0);
    printf("KILLCRED-DONE\n");
    return 0;
}
