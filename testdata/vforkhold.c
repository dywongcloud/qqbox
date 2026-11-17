#define _GNU_SOURCE
#include <sched.h>
#include <stdio.h>
#include <sys/mman.h>
#include <sys/wait.h>
#include <unistd.h>

static volatile int *finished;

static int child(void *unused) {
    usleep(30000);
    *finished = 1;
    _exit(0);
}

int main(void) {
    finished = mmap(NULL, 4096, PROT_READ | PROT_WRITE,
                    MAP_SHARED | MAP_ANONYMOUS, -1, 0);
    char *stack = mmap(NULL, 65536, PROT_READ | PROT_WRITE,
                       MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (finished == MAP_FAILED || stack == MAP_FAILED) return 1;
    for (int mode = 0; mode < 2; ++mode) {
        *finished = 0;
        // Both a non-VM clone with VFORK and the zero-stack vfork fallback
        // must hold the caller. Shared memory makes the ordering observable
        // even when the sandbox implements the child with a real fork.
        pid_t pid;
        if (mode == 0) {
            pid = clone(child, stack + 65536, CLONE_VFORK | SIGCHLD, NULL);
        } else {
            pid = vfork();
            if (pid == 0) child(NULL);
        }
        int held = *finished, status;
        if (pid < 0 || waitpid(pid, &status, 0) != pid || status || !held) {
            fprintf(stderr, "vfork hold failed: mode=%d held=%d\n", mode, held);
            return 2;
        }
    }
    puts("vfork hold OK");
    return 0;
}
