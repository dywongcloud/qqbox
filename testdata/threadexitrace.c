// A process ends (exit_group) while its threads are exiting one by one. The
// kernel marks a thread's end on its CLONE_CHILD_CLEARTID word only while
// another thread of the process remains, so a thread that turns out to be the
// last leaves its word set; the supervisor must not wait on it forever.
#include <pthread.h>
#include <stdio.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

static void *quit(void *arg) {
    syscall(SYS_exit, 0);
    return arg;
}

int main(void) {
    for (int i = 0; i < 400; i++) {
        pid_t pid = fork();
        if (pid == 0) {
            pthread_t t;
            for (int j = 0; j < 4; j++) pthread_create(&t, NULL, quit, NULL);
            syscall(SYS_exit_group, 0);
        }
        int status;
        if (waitpid(pid, &status, 0) != pid) return 1;
    }
    puts("threadexitrace ok");
    return 0;
}
