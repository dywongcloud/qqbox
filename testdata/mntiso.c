// mntiso.c -- a child unshares a mount namespace, mounts a tmpfs at /mnt/iso and
// drops a marker in it; the root-namespace parent must NOT see that mount. The
// child pauses (holding its namespace alive) until the parent has checked.
#define _GNU_SOURCE
#include <stdio.h>
#include <string.h>
#include <errno.h>
#include <sched.h>
#include <unistd.h>
#include <fcntl.h>
#include <signal.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/wait.h>

int main(void) {
    mkdir("/mnt", 0755);
    mkdir("/mnt/iso", 0755);
    int p[2];
    if (pipe(p) != 0) { printf("pipe: %s\n", strerror(errno)); return 1; }
    pid_t c = fork();
    if (c == 0) {
        close(p[0]);
        if (unshare(CLONE_NEWNS) != 0) { printf("unshare: %s\n", strerror(errno)); _exit(3); }
        if (mount("none", "/mnt/iso", "tmpfs", 0, "") != 0) { printf("mount: %s\n", strerror(errno)); _exit(4); }
        int fd = open("/mnt/iso/child-marker", O_CREAT | O_WRONLY, 0644);
        if (fd >= 0) close(fd);
        struct stat sb;
        printf("child-sees-own-mount=%d\n", stat("/mnt/iso/child-marker", &sb) == 0);
        fflush(stdout);
        char r = 'R';
        if (write(p[1], &r, 1) != 1) _exit(5);
        pause();
        _exit(0);
    }
    close(p[1]);
    char b;
    if (read(p[0], &b, 1) != 1) { printf("child not ready\n"); kill(c, SIGKILL); waitpid(c, 0, 0); return 1; }
    struct stat sb;
    printf("parent-sees-child-mount=%d\n", stat("/mnt/iso/child-marker", &sb) == 0);
    kill(c, SIGKILL);
    waitpid(c, 0, 0);
    printf("MNTISO-DONE\n");
    return 0;
}
