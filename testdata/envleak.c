// envleak.c -- prints how many environment entries it has and how many bytes
// /proc/self/environ holds. Run with a nil Config.Env, both must be zero: the
// guest must not inherit the supervisor's environment.
#include <stdio.h>
#include <unistd.h>
#include <fcntl.h>
extern char **environ;
int main(void) {
    int n = 0;
    for (char **e = environ; e && *e; e++) n++;
    printf("envc=%d\n", n);
    char buf[8192];
    int fd = open("/proc/self/environ", O_RDONLY);
    long r = (fd >= 0) ? read(fd, buf, sizeof buf) : 0;
    if (r < 0) r = 0;
    printf("environ_bytes=%ld\n", r);
    printf("ENVLEAK-DONE\n");
    return 0;
}
