#define _GNU_SOURCE
#include <errno.h>
#include <pthread.h>
#include <spawn.h>
#include <stdio.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

extern char **environ;
static char *self;

static void *busy(void *unused) {
    for (;;) usleep(1000);
    return NULL;
}

static void *execer(void *unused) {
    execl(self, self, "spawn", NULL);
    _exit(10);
}

static int spawn(void) {
    int p[2], status;
    pid_t child;
    if (pipe(p)) return 11;
    posix_spawn_file_actions_t actions;
    posix_spawn_file_actions_init(&actions);
    posix_spawn_file_actions_adddup2(&actions, p[1], STDOUT_FILENO);
    posix_spawn_file_actions_addclose(&actions, p[0]);
    posix_spawn_file_actions_addclose(&actions, p[1]);
    char *args[] = {self, "leaf", NULL};
    int err = posix_spawn(&child, self, &actions, NULL, args, environ);
    posix_spawn_file_actions_destroy(&actions);
    close(p[1]);
    if (err) return 12;
    char buf[8] = {0};
    int n = read(p[0], buf, sizeof buf);
    close(p[0]);
    if (waitpid(child, &status, 0) != child || status != 0 ||
        n != 3 || memcmp(buf, "ok\n", 3)) return 13;
    // A failed exec must leave the vfork parent parked until child exit.
    err = posix_spawn(&child, "/nonexistent", NULL, NULL, args, environ);
    return err == ENOENT ? 0 : 14;
}

static void *spawn_thread(void *unused) {
    if (spawn()) _exit(15);
    return NULL;
}

int main(int argc, char **argv) {
    self = argv[0];
    if (argc > 1 && !strcmp(argv[1], "leaf")) {
        return write(1, "ok\n", 3) == 3 ? 0 : 16;
    }
    if (argc > 1 && !strcmp(argv[1], "spawn")) {
        int err = spawn();
        if (err) return err;
        pthread_t thread;
        if (pthread_create(&thread, NULL, spawn_thread, NULL)) return 17;
        pthread_join(thread, NULL);
        return 0;
    }
    if (argc > 1) {
        pthread_t threads[3];
        for (int i = 0; i < 3; ++i)
            if (pthread_create(&threads[i], NULL, busy, NULL)) return 18;
        if (!strcmp(argv[1], "worker")) {
            pthread_t thread;
            if (pthread_create(&thread, NULL, execer, NULL)) return 19;
            for (;;) pause();
        }
        execer(NULL);
    }
    // Fork first: the sandbox's initial process uses the run's done channel,
    // whereas descendants have the process stop channel shared by threads.
    char *modes[] = {"leader", "worker"};
    for (int i = 0; i < 2; ++i) {
        pid_t child = fork();
        if (child == 0) {
            execl(self, self, modes[i], NULL);
            _exit(20);
        }
        int status;
        if (child < 0 || waitpid(child, &status, 0) != child || status != 0) {
            fprintf(stderr, "%s failed: status=%#x\n", modes[i], status);
            return 21;
        }
    }
    puts("exec then spawn OK");
    return 0;
}
