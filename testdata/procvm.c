// procvm.c -- exercises process_vm_readv/writev against the caller itself
// (same address space), checking the supervisor's flat-stream walker: a plain
// copy, a multi-iovec scatter, a writev, and a short count when the remote side
// is smaller. Prints PROCVM-OK on success.
#define _GNU_SOURCE
#include <sys/uio.h>
#include <sys/syscall.h>
#include <unistd.h>
#include <string.h>
#include <stdio.h>

static long readv_(pid_t p, struct iovec *l, int ln, struct iovec *r, int rn) {
    return syscall(SYS_process_vm_readv, p, l, ln, r, rn, 0UL);
}
static long writev_(pid_t p, struct iovec *l, int ln, struct iovec *r, int rn) {
    return syscall(SYS_process_vm_writev, p, l, ln, r, rn, 0UL);
}

int main(void) {
    pid_t me = getpid();
    char src[100], dst[100];
    for (int i = 0; i < 100; i++) { src[i] = (char)i; dst[i] = 0; }

    // plain readv: remote src -> local dst
    struct iovec l = {dst, 100}, r = {src, 100};
    long n = readv_(me, &l, 1, &r, 1);
    if (n != 100 || memcmp(src, dst, 100)) { printf("FAIL plain n=%ld\n", n); return 1; }

    // scatter: one remote seg into two local segs
    char a[40], b[60];
    struct iovec locs[2] = {{a, 40}, {b, 60}};
    struct iovec rem = {src, 100};
    n = readv_(me, locs, 2, &rem, 1);
    if (n != 100 || memcmp(a, src, 40) || memcmp(b, src + 40, 60)) { printf("FAIL scatter n=%ld\n", n); return 1; }

    // writev: local s2 -> remote d2
    char s2[50], d2[50];
    for (int i = 0; i < 50; i++) { s2[i] = (char)(100 + i); d2[i] = 0; }
    struct iovec l2 = {s2, 50}, r2 = {d2, 50};
    n = writev_(me, &l2, 1, &r2, 1);
    if (n != 50 || memcmp(s2, d2, 50)) { printf("FAIL writev n=%ld\n", n); return 1; }

    // short count: remote (10) smaller than local (100)
    char big[100], small[10];
    struct iovec lbig = {big, 100}, rsmall = {small, 10};
    n = readv_(me, &lbig, 1, &rsmall, 1);
    if (n != 10) { printf("FAIL short n=%ld\n", n); return 1; }

    printf("PROCVM-OK\n");
    return 0;
}
