// A process whose leader thread exits first ends when its last thread exits.
// Each child below does that with raw exit calls, as a runtime that manages
// its own threads does (libc would make the last one exit_group); if its end
// were lost the parent would run out of processes long before the loop ends.
#include <pthread.h>
#include <stdio.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

static void *late(void *arg) {
    usleep(1000);
    syscall(SYS_exit, 0);
    return arg;
}

int main(void) {
    for (int i = 0; i < 300; i++) {
        pid_t pid = fork();
        if (pid < 0) {
            printf("fork %d failed\n", i);
            return 1;
        }
        if (pid == 0) {
            pthread_t t;
            if (pthread_create(&t, NULL, late, NULL)) _exit(2);
            syscall(SYS_exit, 0);
        }
        int status;
        if (waitpid(pid, &status, 0) != pid || !WIFEXITED(status) || WEXITSTATUS(status) != 0) {
            printf("child %d: status %#x\n", i, status);
            return 1;
        }
    }
    printf("leaderexit ok\n");
    return 0;
}
